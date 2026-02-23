package main

import (
	"context"
	"klog/cmd"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// 建立唯一的 Root Context，它是整个系统的生命周期源头
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 传递到命令层
	if err := cmd.Execute(ctx); err != nil {
		os.Exit(1)
	}
}
