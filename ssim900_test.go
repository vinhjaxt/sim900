package sim900_test

import (
	"testing"

	"github.com/vinhjaxt/sim900"
)

func TestSendSMS(t *testing.T) {
	ss := sim900.New()
	err := ss.Connect("COM23", 460800)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ss.SendSMS("+84902107790", "Vịnh")
	if err != nil {
		t.Fatal(err)
	}
}
