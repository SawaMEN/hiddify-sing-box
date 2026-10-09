package yandex

import (
	"github.com/sagernet/sing-box/transport/compat/openflux/transport"
)

var _ transport.CookieExchanger = (*YandexDocsTransport)(nil)
