package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP devolve o endereço do cliente (DEC-27 item 11). Sem proxies
// confiáveis configurados, é sempre o endereço da conexão: os cabeçalhos de
// proxy são falsificáveis. Só quando a conexão vem de um proxy confiável se
// usa CF-Connecting-IP, ou o primeiro endereço não confiável de
// X-Forwarded-For lido da direita para a esquerda.
//
// untrustedProxyHeader é verdadeiro quando há cabeçalho de proxy numa
// conexão que não é confiável: sinal de que falta configurar
// COBOGO_TRUSTED_PROXIES (ou de alguém tentando forjar o IP).
func ClientIP(r *http.Request, trusted []netip.Prefix) (ip netip.Addr, untrustedProxyHeader bool) {
	peer := peerAddr(r)
	hasProxyHeader := r.Header.Get("CF-Connecting-IP") != "" || r.Header.Get("X-Forwarded-For") != ""
	if !isTrusted(peer, trusted) {
		return peer, hasProxyHeader
	}

	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		if addr, err := netip.ParseAddr(cf); err == nil {
			return addr.Unmap(), false
		}
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		if !isTrusted(addr, trusted) {
			return addr, false
		}
	}
	return peer, false
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
