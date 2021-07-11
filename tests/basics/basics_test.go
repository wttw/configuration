package basics

import (
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/pflag"
	"net"
	"testing"
)

//go:generate ../../configuration --parseTests --out basics.gen_test.go Basics

type Basics struct {
	// address:port to listen on
	Listen string
	FileCount int
	Files []string
	Things map[string]string `config:"-"`
	IP net.IP
}

func TestNoChange(t *testing.T) {
	want := Basics{
		Listen:    "10.10.10.10:25",
		FileCount: 0,
		Files:     nil,
		Things:    nil,
		IP:        nil,
	}

	got := Basics{
		Listen:    "10.10.10.10:25",
		FileCount: 0,
		Files:     nil,
		Things:    nil,
		IP:        nil,
	}

	flagset := pflag.NewFlagSet("basics", pflag.ExitOnError)
	got.Load([]string{"basics"}, flagset, map[string]string{})

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NoChange mismatch (-want +got):\n%s", diff)
	}
}

func TestAssorted(t *testing.T) {
	want := Basics{
		Listen:    "10.10.10.10:25",
		FileCount: 0,
		Files:     nil,
		Things:    nil,
		IP:       net.IPv4(10,11,12,13),
	}

	got := Basics{
		Listen:    "10.10.10.10:25",
		FileCount: 0,
		Files:     nil,
		Things:    nil,
		IP:        nil,
	}

	flagset := pflag.NewFlagSet("basics", pflag.ExitOnError)
	got.Load([]string{"basics", "--ip=10.11.12.13"}, flagset, map[string]string{})

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Assorted mismatch (-want +got):\n%s", diff)
	}
}