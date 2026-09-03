package eventapi

import "testing"

func TestNopPublisher(t *testing.T) {
	var p Publisher = Nop{}
	if err := p.Publish("x", nil); err != nil {
		t.Fatal(err)
	}
}
