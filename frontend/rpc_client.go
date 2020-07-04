package frontend

import (
	"net"

	"github.com/btcsuite/btcd/rpcclient"
	"github.com/pkg/errors"
	ini "gopkg.in/ini.v1"
)

func NewZRPCFromConf(confPath string) (*rpcclient.Client, error) {
	cfg, err := ini.Load(confPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read config file")
	}

	rpcaddr := cfg.Section("").Key("rpcbind").String()
	rpcport := cfg.Section("").Key("rpcport").String()
	username := cfg.Section("").Key("rpcuser").String()
	password := cfg.Section("").Key("rpcpassword").String()

	//set local default
	if rpcaddr == "" {
		rpcaddr = "127.0.0.1"
	}

	//set default zero rpc port
	if rpcport == "" {
		rpcport = "23811"
	}

	//username and password required
	if username == "" || password == "" {
		return nil, errors.Wrap(err, "username and/or password are not set in config file")
	}

	return NewZRPCFromCreds(net.JoinHostPort(rpcaddr, rpcport), username, password)
}

func NewZRPCFromCreds(addr, username, password string) (*rpcclient.Client, error) {
	// Connect to local zcash RPC server using HTTP POST mode.
	connCfg := &rpcclient.ConnConfig{
		Host:         addr,
		User:         username,
		Pass:         password,
		HTTPPostMode: true, // Zcash only supports HTTP POST mode
		DisableTLS:   true, // Zcash does not provide TLS by default
	}
	// Notice the notification parameter is nil since notifications are
	// not supported in HTTP POST mode.
	return rpcclient.New(connCfg, nil)
}
