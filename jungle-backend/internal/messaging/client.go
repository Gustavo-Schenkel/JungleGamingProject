package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
)

// Queue names refer to three distinct FIFO queues: commands, events, DLQ.
type Queue struct {
	client                 *sqs.Client
	cfg                    config.Config
	inbound, outbound, dlq string
}

func NewQueue(cfg config.Config) (*Queue, error) {
	q := &Queue{cfg: cfg}
	if !cfg.SQSEnabled {
		return q, nil
	}
	if !strings.HasSuffix(cfg.SQSIncomingQueue, ".fifo") || !strings.HasSuffix(cfg.SQSOutgoingQueue, ".fifo") || !strings.HasSuffix(cfg.SQSDLQName, ".fifo") {
		return nil, fmt.Errorf("SQS queue names must end in .fifo")
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.SQSRegion)}
	if cfg.SQSEndpoint != "" {
		u, err := url.Parse(cfg.SQSEndpoint)
		if err != nil || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("invalid SQS_ENDPOINT")
		}
		if cfg.Environment != "development" || (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1")) {
			return nil, fmt.Errorf("non-production localhost SQS_ENDPOINT only allowed in development; use real AWS endpoint with IAM otherwise")
		}
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	}
	sdkCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	q.client = sqs.NewFromConfig(sdkCfg, func(o *sqs.Options) {
		if cfg.SQSEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.SQSEndpoint)
		}
	})
	return q, nil
}

// Connect resolves already provisioned queues; it never changes their settings.
func (q *Queue) Connect(ctx context.Context) error { return q.findQueues(ctx) }

func (q *Queue) findQueues(ctx context.Context) error {
	if !q.cfg.SQSEnabled {
		return nil
	}
	names := []string{q.cfg.SQSIncomingQueue, q.cfg.SQSOutgoingQueue, q.cfg.SQSDLQName}
	urls := make([]string, len(names))
	for i, name := range names {
		out, err := q.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
		if err != nil {
			return fmt.Errorf("get SQS queue %s (run setup-sqs.ps1): %w", name, err)
		}
		urls[i] = aws.ToString(out.QueueUrl)
	}
	q.inbound, q.outbound, q.dlq = urls[0], urls[1], urls[2]
	return nil
}

// ProvisionQueues can be run repeatedly without deleting existing messages.
// SetQueueAttributes reasserts the DLQ redrive policy if needed.
func (q *Queue) ProvisionQueues(ctx context.Context) error {
	if !q.cfg.SQSEnabled {
		return errors.New("set SQS_ENABLED=true")
	}
	dlqURL, err := q.ensureFIFO(ctx, q.cfg.SQSDLQName, map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false", "MessageRetentionPeriod": "1209600"})
	if err != nil {
		return fmt.Errorf("create DLQ: %w", err)
	}
	arnResp, err := q.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: aws.String(dlqURL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn}})
	if err != nil {
		return fmt.Errorf("read DLQ ARN: %w", err)
	}
	arn := arnResp.Attributes["QueueArn"]
	if arn == "" {
		return errors.New("DLQ ARN missing")
	}
	redriveBytes, _ := json.Marshal(map[string]string{"deadLetterTargetArn": arn, "maxReceiveCount": "4"})
	incomingURL, err := q.ensureFIFO(ctx, q.cfg.SQSIncomingQueue, map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false", "VisibilityTimeout": "30"})
	if err != nil {
		return fmt.Errorf("create inbound FIFO: %w", err)
	}
	_, err = q.client.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{QueueUrl: aws.String(incomingURL), Attributes: map[string]string{"RedrivePolicy": string(redriveBytes)}})
	if err != nil {
		return fmt.Errorf("configure FIFO redrive: %w", err)
	}
	_, err = q.ensureFIFO(ctx, q.cfg.SQSOutgoingQueue, map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false"})
	if err != nil {
		return fmt.Errorf("create outbound FIFO: %w", err)
	}
	return q.findQueues(ctx)
}

func (q *Queue) SendCommand(ctx context.Context, e SignedCommand) error {
	signed, err := SignCommand(q.cfg, e)
	if err != nil {
		return err
	}
	if q.inbound == "" {
		return errors.New("SQS inbound queue not initialized")
	}
	data, err := json.Marshal(signed)
	if err != nil {
		return err
	}
	// Fresh SQS message each time; DB inbox and financial idempotency handle duplicates.
	_, err = q.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: aws.String(q.inbound), MessageBody: aws.String(string(data)),
		MessageGroupId: aws.String(e.Command.WalletID), MessageDeduplicationId: aws.String(freshDedupID()),
	})
	return err
}

func (q *Queue) ensureFIFO(ctx context.Context, name string, attrs map[string]string) (string, error) {
	found, err := q.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err == nil {
		return aws.ToString(found.QueueUrl), nil
	}
	created, err := q.client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name), Attributes: attrs})
	if err != nil {
		return "", err
	}
	return aws.ToString(created.QueueUrl), nil
}
