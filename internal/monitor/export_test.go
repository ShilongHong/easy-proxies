package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"easy_proxies/internal/config"
)

type exportNodeManager struct {
	NodeManager
	nodes []config.NodeConfig
	err   error
}

func (m exportNodeManager) ListConfigNodes(context.Context) ([]config.NodeConfig, error) {
	return m.nodes, m.err
}

func TestExportUsesRuntimePortsWithoutMonitorEntries(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mgr: mgr, cfgSrc: &config.Config{Mode: "multi-port", MultiPort: config.MultiPortConfig{Address: "127.0.0.1"}}, nodeMgr: exportNodeManager{nodes: []config.NodeConfig{{Port: 24000}, {Port: 24001}, {Port: 0}}}}
	for _, scheme := range []string{"http", "socks5", "all"} {
		w := httptest.NewRecorder()
		s.handleExport(w, httptest.NewRequest("GET", "/api/export?scheme="+scheme, nil))
		want := []string{}
		for _, port := range []string{"24000", "24001"} {
			if scheme != "socks5" {
				want = append(want, "http://127.0.0.1:"+port)
			}
			if scheme != "http" {
				want = append(want, "socks5://127.0.0.1:"+port)
			}
		}
		if w.Code != 200 || w.Body.String() != strings.Join(want, "\n") {
			t.Fatalf("%s: status=%d body=%q", scheme, w.Code, w.Body.String())
		}
	}
	s.cfgSrc.MultiPort.Address = "::1"
	s.cfgSrc.MultiPort.Username = "u@x"
	s.cfgSrc.MultiPort.Password = "p:x"
	w := httptest.NewRecorder()
	s.handleExport(w, httptest.NewRequest("GET", "/api/export", nil))
	if !strings.Contains(w.Body.String(), "http://u%40x:p%3Ax@[::1]:24000") {
		t.Fatal(w.Body.String())
	}
	s.nodeMgr = exportNodeManager{err: errors.New("unavailable")}
	w = httptest.NewRecorder()
	s.handleExport(w, httptest.NewRequest("GET", "/api/export", nil))
	if w.Code != 500 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestExportSub2APIIncludesRuntimeNamesAndPorts(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		mgr: mgr,
		cfgSrc: &config.Config{
			Mode:      "multi-port",
			MultiPort: config.MultiPortConfig{Address: "0.0.0.0", BasePort: 24000},
		},
		nodeMgr: exportNodeManager{nodes: []config.NodeConfig{
			{Name: "🇦🇲 亚美尼亚", Port: 24001},
			{Name: "🇦🇴 安哥拉", Port: 24002},
		}},
	}
	w := httptest.NewRecorder()
	s.handleExport(w, httptest.NewRequest("GET", "/api/export?format=sub2api&scheme=http&host=172.22.0.1", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got sub2APIExport
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Proxies) != 2 || got.Proxies[0].Name != "🇦🇲 亚美尼亚" || got.Proxies[0].Host != "172.22.0.1" || got.Proxies[0].Port != 24001 {
		t.Fatalf("unexpected export: %#v", got.Proxies)
	}
	if got.Proxies[0].ProxyKey != "http|172.22.0.1|24001||" {
		t.Fatalf("proxy key = %q", got.Proxies[0].ProxyKey)
	}
}
