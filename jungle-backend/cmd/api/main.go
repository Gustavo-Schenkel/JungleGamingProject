package main

import (
	"github.com/gustavo-schenkel/jungle-backend-challenge/internal/app"
	"go.uber.org/fx"
	"time"
)

func main() { fx.New(app.Module, fx.StartTimeout(120*time.Second)).Run() }
