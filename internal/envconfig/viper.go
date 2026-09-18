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

// Lookup resolves an explicitly named environment value through the shared
// configuration adapter. It is used for connection-URI secret indirection,
// where the URI carries the variable name but must not carry the secret.
func Lookup(key string) (string, bool) {
	v := NewViper("")
	if err := v.BindEnv(key); err != nil || !v.IsSet(key) {
		return "", false
	}
	return v.GetString(key), true
}
