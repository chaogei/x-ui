package model

import (
	"strconv"
	"testing"
)

var benchmarkSettingsSink string

func BenchmarkApplyClients(b *testing.B) {
	cases := []struct {
		name     string
		protocol Protocol
		settings string
		clients  []*Client
	}{
		{
			name:     "vmess/1_client",
			protocol: VMess,
			settings: `{"users":[{"name":"legacy","uuid":"legacy"}],"tls":{"enabled":false}}`,
			clients:  benchmarkClients(1, false),
		},
		{
			name:     "vmess/10_clients",
			protocol: VMess,
			settings: `{"users":[{"name":"legacy","uuid":"legacy"}],"tls":{"enabled":false}}`,
			clients:  benchmarkClients(10, false),
		},
		{
			name:     "vmess/100_clients",
			protocol: VMess,
			settings: `{"users":[{"name":"legacy","uuid":"legacy"}],"tls":{"enabled":false}}`,
			clients:  benchmarkClients(100, false),
		},
		{
			name:     "vless/10_clients_with_extra",
			protocol: VLESS,
			settings: `{"users":[],"tls":{"enabled":true}}`,
			clients:  benchmarkClients(10, true),
		},
		{
			name:     "tuic/10_clients",
			protocol: TUIC,
			settings: `{"users":[],"congestion_control":"bbr","tls":{"enabled":true}}`,
			clients:  benchmarkClients(10, false),
		},
		{
			name:     "shadowsocks/1_client",
			protocol: Shadowsocks,
			settings: `{"method":"2022-blake3-aes-128-gcm","password":"legacy"}`,
			clients:  benchmarkClients(1, false),
		},
		{
			name:     "wireguard/passthrough",
			protocol: WireGuard,
			settings: `{"private_key":"key","address":["10.0.0.1/32"],"peers":[]}`,
			clients:  benchmarkClients(100, false),
		},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			var (
				out string
				err error
			)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err = ApplyClients(tc.protocol, tc.settings, tc.clients)
			}
			if err != nil {
				b.Fatal(err)
			}
			benchmarkSettingsSink = out
		})
	}
}

func benchmarkClients(count int, withExtra bool) []*Client {
	clients := make([]*Client, count)
	for i := range clients {
		suffix := strconv.Itoa(i)
		extra := ""
		if withExtra {
			extra = `{"flow":"xtls-rprx-vision"}`
		}
		clients[i] = &Client{
			Email:    "client-" + suffix + "@example.com",
			UUID:     "00000000-0000-0000-0000-" + leftPad12(suffix),
			Password: "password-" + suffix,
			Username: "username-" + suffix,
			Extra:    extra,
		}
	}
	return clients
}

func leftPad12(value string) string {
	const zeros = "000000000000"
	if len(value) >= len(zeros) {
		return value
	}
	return zeros[:len(zeros)-len(value)] + value
}
