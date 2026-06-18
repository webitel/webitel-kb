package tls

import (
	"crypto/tls"
	"crypto/x509"
	"os"

	"go.uber.org/fx"

	"github.com/webitel/webitel-kb/config"
)

var Module = fx.Module("tls",
	fx.Provide(
		ProvideTLSConfig,
	))

type Config struct {
	Client *tls.Config
	Server *tls.Config
}

func ProvideTLSConfig(cfg *config.Config) (*Config, error) {
	var (
		connConfig = cfg.Service.Connection
		conf       = &Config{}
		authType   = tls.RequireAndVerifyClientCert
		err        error
	)

	if !connConfig.VerifyCerts {
		return conf, nil
	}

	conf.Server, err = Load(connConfig.TLSConfig, authType)
	if err != nil {
		return nil, err
	}
	conf.Client, err = Load(connConfig.Client, tls.NoClientCert)
	if err != nil {
		return nil, err
	}

	return conf, nil
}

func Load(connConfig config.TLSConfig, authType tls.ClientAuthType) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(connConfig.Cert, connConfig.Key)
	if err != nil {
		return nil, err
	}

	caCert, err := os.ReadFile(connConfig.CA)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   authType,
		RootCAs:      caCertPool,
	}, nil
}
