package main

import (
	"context"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/log"
	"fernandoglatz/aws-infrastructure-helper/internal/core/service"

	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config"

	"github.com/joho/godotenv"
)

func main() {
	ctx := context.Background()
	godotenv.Load()

	err := config.LoadConfig(ctx)
	if err != nil {
		log.Fatal(ctx).Msg(err.Error())
	}

	helperService := service.NewHelperService()
	err = helperService.ScheduleDNSCheck(ctx)
	if err != nil {
		log.Fatal(ctx).Msg(err.Error())
	}

	select {}
}
