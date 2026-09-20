package builder

import (
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/outbound/dispatch"
	poolout "easy_proxies/internal/outbound/pool"
	"easy_proxies/internal/proxychain"

	"github.com/sagernet/sing-box/option"
)

func TestBuildMultiPortKeepsEveryPortWhenNamesCollide(t *testing.T) {
	for _, names := range [][]string{
		{"mesl-日本", "mesl-香港", "mesl-2"},
		{"mesl-2", "mesl-日本", "mesl-香港", "mesl-台湾"},
		{"node", "node", "node-2", "node-2", "node-2-2", "node"},
	} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			cfg := &config.Config{Mode: "multi-port", MultiPort: config.MultiPortConfig{Address: "127.0.0.1"}}
			for i, name := range names {
				cfg.Nodes = append(cfg.Nodes, config.NodeConfig{Name: name, URI: "http://127.0.0.1:18001", Port: uint16(24000 + i)})
			}
			options, err := Build(cfg)
			if err != nil {
				t.Fatal(err)
			}
			outboundTags := make(map[string]bool)
			for _, outbound := range options.Outbounds {
				if outboundTags[outbound.Tag] {
					t.Fatalf("duplicate outbound tag %q", outbound.Tag)
				}
				outboundTags[outbound.Tag] = true
			}
			ports := make(map[uint16]bool)
			inboundTags := make(map[string]bool)
			for _, inbound := range options.Inbounds {
				port := inbound.Options.(*option.HTTPMixedInboundOptions).ListenPort
				if ports[port] || inboundTags[inbound.Tag] {
					t.Fatalf("duplicate listener: tag=%q port=%d", inbound.Tag, port)
				}
				ports[port], inboundTags[inbound.Tag] = true, true
			}
			for _, node := range cfg.Nodes {
				if !ports[node.Port] {
					t.Errorf("missing port %d", node.Port)
				}
			}
			for _, outbound := range options.Outbounds {
				if outbound.Type == dispatch.Type {
					mappings := outbound.Options.(*dispatch.Options).Mappings
					if len(mappings) != len(names) {
						t.Fatalf("dispatch mappings = %d, want %d", len(mappings), len(names))
					}
					for inbound, target := range mappings {
						if !inboundTags[inbound] || !outboundTags[target] {
							t.Fatalf("invalid dispatch mapping %q -> %q", inbound, target)
						}
					}
				}
			}
		})
	}
}

func TestBuildMultiPortUsesDirectOutboundsAndDispatch(t *testing.T) {
	cfg := &config.Config{
		Mode:      "multi-port",
		LogLevel:  "error",
		MultiPort: config.MultiPortConfig{Address: "127.0.0.1", BasePort: 12000},
		Pool: config.PoolConfig{
			Mode:              "balance",
			FailureThreshold:  2,
			BlacklistDuration: time.Minute,
			RotationInterval:  time.Minute,
		},
		Nodes: []config.NodeConfig{
			{Name: "node-a", URI: "http://127.0.0.1:18001", Port: 12001},
			{Name: "node-b", URI: "socks5://127.0.0.1:18002", Port: 12002},
		},
	}

	options, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(options.Inbounds) != len(cfg.Nodes) {
		t.Fatalf("inbounds = %d, want %d", len(options.Inbounds), len(cfg.Nodes))
	}
	if len(options.Outbounds) != len(cfg.Nodes)+1 {
		t.Fatalf("outbounds = %d, want %d node outbounds + dispatcher", len(options.Outbounds), len(cfg.Nodes)+1)
	}
	poolCount := 0
	for _, outbound := range options.Outbounds {
		if outbound.Type != poolout.Type {
			continue
		}
		poolCount++
		if outbound.Tag != poolout.Tag {
			t.Fatalf("unexpected per-node pool tag %q", outbound.Tag)
		}
		poolOptions, ok := outbound.Options.(*poolout.Options)
		if !ok || len(poolOptions.Members) != len(cfg.Nodes) {
			t.Fatalf("monitor pool options = %#v", outbound.Options)
		}
	}
	if poolCount != 0 {
		t.Fatalf("pool outbounds = %d, want 0", poolCount)
	}
	if len(options.Route.Rules) != 0 {
		t.Fatalf("multi-port route rules = %d, want 0", len(options.Route.Rules))
	}
	if options.Route.Final != dispatch.Tag {
		t.Fatalf("route final = %q, want %q", options.Route.Final, dispatch.Tag)
	}
	var dispatchOptions *dispatch.Options
	for _, outbound := range options.Outbounds {
		if outbound.Type == dispatch.Type {
			dispatchOptions, _ = outbound.Options.(*dispatch.Options)
		}
	}
	if dispatchOptions == nil || len(dispatchOptions.Mappings) != len(cfg.Nodes) {
		t.Fatalf("dispatcher mappings = %#v", dispatchOptions)
	}
}

func TestValidateNodeConfigsReportsBuildFailuresWithoutURIs(t *testing.T) {
	cfg := &config.Config{
		Mode: "multi-port",
		Nodes: []config.NodeConfig{
			{Name: "valid", URI: "http://127.0.0.1:18001", Port: 12001},
			{Name: "invalid", URI: "unsupported://127.0.0.1:18002", Port: 12002},
		},
	}
	failures := ValidateNodeConfigs(cfg)
	if len(failures) != 1 || failures[0].Name != "invalid" || failures[0].Port != 12002 {
		t.Fatalf("failures = %#v, want one failure for invalid node", failures)
	}
	if strings.Contains(failures[0].Reason, "18002") {
		t.Fatalf("failure reason exposes node endpoint: %q", failures[0].Reason)
	}
}

func TestValidateNodeConfigsUsesTheRuntimeChainShape(t *testing.T) {
	cfg := &config.Config{
		Mode: "multi-port",
		ChainProfiles: []proxychain.Profile{{
			ID: "front", Name: "front", Enabled: true,
			Hops: []proxychain.Hop{{URI: "http://127.0.0.1:18080"}},
		}},
		Nodes: []config.NodeConfig{{
			Name: "terminal", URI: "http://127.0.0.1:18081", Port: 12001, ChainProfileID: "front",
		}},
	}
	if failures := ValidateNodeConfigs(cfg); len(failures) != 0 {
		t.Fatalf("chain validation failures = %#v", failures)
	}
}

func TestCoreLogLevelBoundsLargeMultiPortStartupLogs(t *testing.T) {
	large := &config.Config{Mode: "multi-port", LogLevel: "info"}
	if got := coreLogLevel(large, verboseMultiPortLogLimit+1); got != "warn" {
		t.Fatalf("coreLogLevel() = %q, want warn", got)
	}
	large.LogLevel = "debug"
	if got := coreLogLevel(large, verboseMultiPortLogLimit+1); got != "debug" {
		t.Fatalf("explicit debug level changed to %q", got)
	}
	pool := &config.Config{Mode: "pool", LogLevel: "info"}
	if got := coreLogLevel(pool, verboseMultiPortLogLimit+1); got != "info" {
		t.Fatalf("pool log level changed to %q", got)
	}
}
