package app

import (
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/auth"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/config"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/database"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/finance"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/httpserver"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/messaging"
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/wallet"
	"go.uber.org/fx"
)

var Module = fx.Options(
	fx.Provide(config.Load),
	fx.Provide(auth.NewVerifier),
	fx.Invoke(auth.RegisterLifecycle),
	fx.Provide(database.NewPool),
	fx.Invoke(database.RegisterLifecycle),
	fx.Provide(httpserver.NewMux),
	fx.Provide(httpserver.NewServer),
	fx.Provide(wallet.NewRepository),
	fx.Provide(finance.NewService),
	fx.Provide(messaging.NewQueue),
	fx.Invoke(httpserver.RegisterWalletRoutes),
	fx.Invoke(httpserver.RegisterFinanceRoutes),
	fx.Invoke(httpserver.RegisterProviderRoutes),
	fx.Invoke(finance.RegisterReferenceWorker),
	fx.Invoke(messaging.RegisterLifecycle),
	fx.Invoke(httpserver.RegisterMessagingRoutes),
	fx.Invoke(httpserver.RegisterLifecycle),
)
