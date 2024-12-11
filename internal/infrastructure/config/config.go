package config

import (
	"context"
	"errors"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/constants"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/log"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config/format"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Listening   string `yaml:"listening"`
		ContextPath string `yaml:"context-path"`
	} `yaml:"server"`

	Application struct {
		DNSUpdater struct {
			Interval time.Duration `yaml:"interval"`

			PublicIPFetcher struct {
				Url     string        `yaml:"url"`
				Timeout time.Duration `yaml:"timeout"`
			} `yaml:"public-ip-fetcher"`

			Record struct {
				HostedZoneId string `yaml:"hosted-zone-id"`
				Name         string `yaml:"name"`
				TTL          int64  `yaml:"ttl"`
			} `yaml:"record"`
		} `yaml:"dns-updater"`
	} `yaml:"application"`

	Aws struct {
		Credentials struct {
			AccessKey string `yaml:"access-key"`
			SecretKey string `yaml:"secret-key"`
		} `yaml:"credentials"`

		Region string `yaml:"region"`
	} `yaml:"aws"`

	Log struct {
		Level   string        `yaml:"level"`
		Format  format.Format `yaml:"format"`
		Colored bool          `yaml:"colored"`
	} `yaml:"log"`
}

type Queue struct {
	Name                 string        `yaml:"name"`
	MaximumReceives      int           `yaml:"maximum-receives"`
	RequeueDelay         time.Duration `yaml:"requeue-delay"`
	RequeueDelayExchange string        `yaml:"requeue-delay-exchange"`
}

var ApplicationConfig Config

func LoadConfig(ctx context.Context) error {
	loadProfile(ctx)

	err := loadLocalConfig(ctx)
	if err != nil {
		return err
	}

	logConfig := ApplicationConfig.Log
	log.ReconfigureLogger(ctx, logConfig.Format, logConfig.Level, logConfig.Colored)

	return nil
}

func IsDevProfile() bool {
	profile := os.Getenv(constants.PROFILE)
	return constants.DEV_PROFILE == profile
}

func loadProfile(ctx context.Context) {
	profile := os.Getenv(constants.PROFILE)
	if len(profile) == constants.ZERO {
		profile = constants.DEV_PROFILE
		os.Setenv(constants.PROFILE, profile)
	}

	log.SetupLogger(profile) //after setup profile
	log.Info(ctx).Msg("Profile loaded: " + profile)
}

func loadLocalConfig(ctx context.Context) error {
	log.Info(ctx).Msg("Loading local config")

	data, err := os.ReadFile("conf/application.yml")
	if err != nil {
		return errors.New("Failed to read configuration file: " + err.Error())
	}

	err = yaml.Unmarshal(data, &ApplicationConfig)
	if err != nil {
		return errors.New("Failed to parse configuration file: " + err.Error())
	}

	log.Info(ctx).Msg("Loaded local config")

	return nil
}
