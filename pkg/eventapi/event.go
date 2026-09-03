package eventapi

// Publisher is the public event contract. Community SSE and outbound
// webhooks stay in internal/api. Enterprise may add SIEM sinks.
type Publisher interface {
	Publish(event string, payload any) error
}

type Nop struct{}

func (Nop) Publish(string, any) error { return nil }
