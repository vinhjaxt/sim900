package sim900

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vinhjaxt/serial"
	"github.com/xlab/at/pdu"
	"github.com/xlab/at/sms"
)

// A SIM900 is the representation of a SIM900 GSM modem with several utility features.
type SIM900 struct {
	PortMu       *sync.RWMutex
	Port         *serial.SerialPort
	logger       *log.Logger
	CSCA         string
	SMSEventLock *sync.RWMutex
	mapSMSEvents map[uint64]func(sms *sms.Message)
	nextSMSEvent uint64
	OnNewCall    func(phoneNumber string)
	OnError      func(err error)
}

// New creates and initializes a new SIM900 device.
func New() *SIM900 {
	return &SIM900{
		Port:         serial.New(),
		PortMu:       &sync.RWMutex{},
		SMSEventLock: &sync.RWMutex{},
		logger:       log.New(os.Stdout, "[sim900] ", log.LstdFlags),
		mapSMSEvents: map[uint64]func(*sms.Message){},
	}
}

// Connect creates a connection with the SIM900 modem via serial port and test communications.
func (s *SIM900) Connect(port string, baud int) error {
	if err := s.Port.Open(port, baud, time.Millisecond*100); err != nil {
		return err
	}
	return s.Init()
}

// Close device serial port
func (s *SIM900) Close() error {
	return s.Port.Close()
}

// Wait4response send command and wait for response
func (s *SIM900) Wait4response(cmd, expected string, timeout time.Duration) ([]string, error) {
	s.PortMu.RLock()
	defer s.PortMu.RUnlock()
	return s.wait4response(cmd, expected, timeout)
}

// wait4response send command and wait for response no lock
func (s *SIM900) wait4response(cmd, expected string, timeout time.Duration) ([]string, error) {
	// Wait for command response
	regexp := expected + `|(^|\W)ERROR($|\W)`
	response, err := s.Port.WaitForRegexTimeout(cmd, regexp, timeout)
	if err != nil {
		return nil, err
	}
	// Check if response is an error
	if strings.Contains(response[0], "ERROR") {
		return response, errors.New("Errors found on command response: " + response[0])
	}
	// Response received succesfully
	return response, nil
}

// SendUSSD send ussd command
func (s *SIM900) SendUSSD(ussd string) (string, error) {
	response, err := s.Wait4response(`AT+CUSD=1,"`+hex.EncodeToString(pdu.Encode7Bit(ussd))+`",15`, `(\+CUSD: \d,"([^"]+)",15)`, time.Second*60)
	if err != nil {
		return "", err
	}
	bs, err := hex.DecodeString(response[2])
	if err != nil {
		return "", err
	}
	return pdu.Decode7Bit(bs)
}

// SendSMS return sms id or error
func (s *SIM900) SendSMS(address, text string) (string, error) {
	s.PortMu.Lock()
	defer s.PortMu.Unlock()

	msg := sms.Message{
		Text:     text,
		Type:     sms.MessageTypes.Submit,
		Encoding: sms.Encodings.Gsm7Bit,
		Address:  sms.PhoneNumber(address),
		VPFormat: sms.ValidityPeriodFormats.Relative,
		VP:       sms.ValidityPeriod(24 * time.Hour * 4),
	}

	if s.CSCA != "" {
		msg.ServiceCenterAddress = sms.PhoneNumber(s.CSCA)
	}

	for _, w := range text {
		if w > 1 {
			msg.Encoding = sms.Encodings.UCS2
			break
		}
	}

	n, octets, err := msg.PDU()
	if err != nil {
		return "", err
	}

	response, err := s.wait4response(fmt.Sprintf("AT+CMGS=%d", n), `(> )|(\+CMS ERROR: \d+($|\W))`, time.Second*3)
	if err != nil {
		return "", err
	}

	response, err = s.wait4response(fmt.Sprintf("%02X", octets)+CMD_CTRL_Z, `(\+CMGS: (\d+)($|\W))|(\+CMS ERROR: \d+($|\W))`, time.Second*60)
	if err != nil {
		return "", err
	}
	return response[2], nil
}

// DelSMSListener remove listener
func (s *SIM900) DelSMSListener(id uint64) {
	s.SMSEventLock.Lock()
	delete(s.mapSMSEvents, id)
	s.SMSEventLock.Unlock()
}

// AddSMSListener add listener
func (s *SIM900) AddSMSListener(fn func(*sms.Message)) uint64 {
	id := atomic.AddUint64(&s.nextSMSEvent, 1)
	s.SMSEventLock.Lock()
	s.mapSMSEvents[id] = fn
	s.SMSEventLock.Unlock()
	return id
}

// WaitSMSText wait for sms match by phone number
func (s *SIM900) WaitSMSText(phoneNumber string, timeout time.Duration, inits ...func() error) (string, error) {
	result := make(chan string)
	defer s.DelSMSListener(s.AddSMSListener(func(msg *sms.Message) {
		if string(msg.Address) == phoneNumber {
			result <- msg.Text
		}
	}))

	for _, init := range inits {
		err := init()
		return "", err
	}

	select {
	case <-time.After(timeout):
		return "", errors.New("timeout waiting sms for: " + phoneNumber)
	case str := <-result:
		return str, nil
	}
}

// WaitSMSFunc wait for sms match by function
func (s *SIM900) WaitSMSFunc(match func(*sms.Message) bool, timeout time.Duration, inits ...func() error) error {
	result := make(chan struct{})
	s.DelSMSListener(s.AddSMSListener(func(msg *sms.Message) {
		if match(msg) {
			result <- struct{}{}
		}
	}))

	for _, init := range inits {
		err := init()
		return err
	}

	select {
	case <-time.After(timeout):
		return errors.New("timeout waiting sms")
	case <-result:
		return nil
	}
}

// Init modem
func (s *SIM900) Init() error {
	time.Sleep(1 * time.Second)
	s.OnError = func(err error) {
		log.Println(err)
	}

	newMessagePattern := regexp.MustCompile(`\+CMT:[\s,\d]+\r?\n([a-zA-Z\d]+)\r?\n`)
	newCallPattern := regexp.MustCompile(`(^|\W)RING(\r?\n)+\+CLIP: "(\d+)"($|\W)`)
	endCallPattern := regexp.MustCompile(`\^CEND:\d+`)
	isRinging := atomic.Value{}
	isRinging.Store(false)

	s.Port.OnRxData = func(b []byte) {
		body := string(b)
		matches := newMessagePattern.FindAllStringSubmatch(body, -1)
		for _, match := range matches {
			if len(match) > 0 {
				// Có tin nhắn tới
				bs, err := hex.DecodeString(strings.Trim(match[1], "\r\n"))
				if err != nil {
					if s.OnError != nil {
						s.OnError(err)
					}
					continue
				}
				msg := new(sms.Message)
				_, err = msg.ReadFrom(bs)
				/* // github.com\xlab\at\sms line 103
				// Alphanumeric, (coded according to GSM TS 03.38 7-bit default alphabet)
				if addrType&0x70 == 0x50 {
					// decode 7 bit
					addr, err := pdu.Decode7Bit(octets[1:])
					if err != nil {
						log.Println("Decode7bit", octets, err)
					}
					*p = PhoneNumber(addr)
					return
				}
				*/
				if err != nil {
					if s.OnError != nil {
						s.OnError(err)
					}
					continue
				}
				go func() {
					s.SMSEventLock.RLock()
					for _, fn := range s.mapSMSEvents {
						go fn(msg)
					}
					s.SMSEventLock.RUnlock()
				}()
			}
		}

		if isRinging.Load().(bool) && endCallPattern.MatchString(body) {
			isRinging.Store(false)
		}

		if isRinging.Load().(bool) == false {
			match := newCallPattern.FindStringSubmatch(body)
			if len(match) > 0 {
				// có cuộc gọi đến
				isRinging.Store(true)
				if s.OnNewCall != nil {
					go s.OnNewCall(match[3])
				}
			}
		}
	}

	// Ping
	_, err := s.Wait4response("AT", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// Dont echo command
	_, err = s.Wait4response("ATE0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// set auto select service
	_, err = s.Wait4response("AT+COPS=0,0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// set sms storage
	_, err = s.Wait4response(`AT+CPMS="ME","ME","ME"`, CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// set pdu mode
	_, err = s.Wait4response("AT+CMGF=0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// Dont store sms, return pdu data
	_, err = s.Wait4response("AT+CNMI=1,2,0,0,0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// Show phone number is calling
	_, err = s.Wait4response("AT+CLIP=1", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// get service number
	csca, err := s.getCSCA()
	if err != nil {
		return err
	}
	s.CSCA = csca

	return nil
}

func (s *SIM900) getCSCA() (string, error) {
	response, err := s.Wait4response("AT+CSCA?", `(\+CSCA:\s*"(\+\d+)"($|\W))`, time.Second*3)
	if err != nil {
		return "", err
	}
	return response[2], nil
}
