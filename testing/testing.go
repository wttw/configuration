package main

import (
	zc "go.uber.org/zap/zapcore"
	"net"
)

//go:generate enumer -type Verbosity
//go:generate ../configuration Config

type Verbosity int

const (
	Normal Verbosity = iota
	Chatty
	Silent
)

type Config struct {
	// address:port to listen on
	Listen string
	FileCount int
	Files []string
	Things map[string]string `config:"-"`
	IP net.IP
	Verbose Verbosity // How chatty to be
	Logging zc.Level // Logging level
	Levels []zc.Level
}

func main() {

}
