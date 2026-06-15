package envconfig

import "github.com/spf13/viper"

type Default struct {
	Key   string
	Value any
}

func NewViper(envPrefix string) *viper.Viper {
	v := viper.New()
	if envPrefix != "" {
		v.SetEnvPrefix(envPrefix)
	}
	v.AutomaticEnv()
	return v
}

func BindEnv(v *viper.Viper, keys ...string) error {
	for _, key := range keys {
		if err := v.BindEnv(key); err != nil {
			return err
		}
	}
	return nil
}

func SetDefaults(v *viper.Viper, defaults ...Default) {
	for _, item := range defaults {
		v.SetDefault(item.Key, item.Value)
	}
}
