package config

import (
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/rclone/rclone/fs"
)

type Config struct {
	ApiURL            string        `envconfig:"API_URL" required:"true"`
	SessionToken      string        `envconfig:"SESSION_TOKEN" required:"true"`
	PartSize          fs.SizeSuffix `envconfig:"PART_SIZE"`
	ChannelID         int64         `envconfig:"CHANNEL_ID"`
	Workers           int           `envconfig:"WORKERS" default:"4"`
	Transfers         int           `envconfig:"TRANSFERS" default:"4"`
	RandomisePart     bool          `envconfig:"RANDOMISE_PART" default:"true"`
	EncryptFiles      bool          `envconfig:"ENCRYPT_FILES" default:"false"`
	DeleteAfterUpload bool          `envconfig:"DELETE_AFTER_UPLOAD" default:"false"`
	Debug             bool          `envconfig:"DEBUG" default:"false"`
}

var config Config

func InitConfig() {

	err := godotenv.Load("upload.env")
	if err != nil {
		panic(err)
	}

	err = envconfig.Process("", &config)
	if err != nil {
		panic(err)
	}
	if config.PartSize == 0 {
		config.PartSize = 1000 * fs.Mebi
	}
}

func GetConfig() *Config {
	return &config
}
