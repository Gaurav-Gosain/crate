package ui

import "github.com/Gaurav-Gosain/crate/internal/config"

func newBenchConfig() *config.Config {
	return &config.Config{Library: "/tmp/none", Format: "opus", Parallel: 4}
}
