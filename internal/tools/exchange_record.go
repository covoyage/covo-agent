package tools

import "github.com/covoyage/covo-agent/internal/cyber"

// ExchangeRecorder persists an HTTP exchange and returns its id (ex-N).
// A nil recorder, or a recorder that returns "", skips evidence linking.
type ExchangeRecorder func(method, url string, status int, reqHeaders, request, response string) string

func catalogExchangeRecorder(catalog *cyber.Catalog) ExchangeRecorder {
	return func(method, url string, status int, reqHeaders, request, response string) string {
		if catalog == nil {
			return ""
		}
		ex, err := catalog.RecordExchange(cyber.Exchange{
			Method: method, URL: url, StatusCode: status,
			ReqHeaders: reqHeaders, Request: request, Response: response,
		})
		if err != nil {
			return ""
		}
		return ex.ID
	}
}
