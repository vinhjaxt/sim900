package sim900

/*
Value	RSSI dBm	Condition
2	-109	Marginal
3	-107	Marginal
4	-105	Marginal
5	-103	Marginal
6	-101	Marginal
7	-99	Marginal
8	-97	Marginal
9	-95	Marginal
10	-93	OK
11	-91	OK
12	-89	OK
13	-87	OK
14	-85	OK
15	-83	Good
16	-81	Good
17	-79	Good
18	-77	Good
19	-75	Good
20	-73	Excellent
21	-71	Excellent
22	-69	Excellent
23	-67	Excellent
24	-65	Excellent
25	-63	Excellent
26	-61	Excellent
27	-59	Excellent
28	-57	Excellent
29	-55	Excellent
30	-53	Excellent
*/

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
	"unicode"

	"github.com/vinhjaxt/serial"
	"github.com/xlab/at/pdu"
	"github.com/xlab/at/sms"
)

// A SIM900 is the representation of a SIM900 GSM modem with several utility features.
type SIM900 struct {
	nextSMSEvent uint64 // move to the first field fix 64bit unaligned pointers atomic
	PortMu       *sync.RWMutex
	Port         *serial.SerialPort
	logger       *log.Logger
	CSCA         string
	SMSEventLock *sync.RWMutex
	mapSMSEvents map[uint64]func(sms *sms.Message)
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
func (s *SIM900) wait4response(cmd, expected string, timeout time.Duration, inits ...func() error) ([]string, error) {
	// Wait for command response
	regexp := expected + `|(^|\W)ERROR($|\W)`
	response, err := s.Port.WaitForRegexTimeout(cmd, regexp, timeout, inits...)
	if err != nil {
		return nil, err
	}
	// Check if response is an error
	if strings.Contains(response[0], "ERROR") {
		return response, errors.New("Error: " + response[0])
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

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// SendSMS return sms id or error
func (s *SIM900) SendSMS(address, text string) (string, error) {
	if len(text) > 160 {
		return "", errors.New("SMS Length > 160")
	}

	s.PortMu.Lock()
	defer s.PortMu.Unlock()
	time.Sleep(5 * time.Second)

	msg := sms.Message{
		Text:                text,
		Type:                sms.MessageTypes.Submit,
		Encoding:            sms.Encodings.Gsm7Bit,
		Address:             sms.PhoneNumber(address),
		VPFormat:            sms.ValidityPeriodFormats.Relative,
		VP:                  sms.ValidityPeriod(63 * 7 * 24 * time.Hour),
		RejectDuplicates:    true,
		StatusReportRequest: true,
	}

	if s.CSCA != "" {
		msg.ServiceCenterAddress = sms.PhoneNumber(s.CSCA)
	}

	if !isASCII(msg.Text) {
		msg.Encoding = sms.Encodings.UCS2
	}

	n, octets, err := msg.PDU()
	if err != nil {
		return "", err
	}

	response, err := s.wait4response("", `(> )|(\+CMS ERROR: \d+($|\W))`, time.Second*3, func() error {
		return s.Port.Print(fmt.Sprintf("AT+CMGS=%d\r", n))
	})
	if err != nil {
		return "", err
	}

	err = s.Port.Print(fmt.Sprintf("%02X", octets))
	if err != nil {
		return "", err
	}

	time.Sleep(100 * time.Millisecond)

	response, err = s.wait4response("", `(\+CMGS: (\d+)($|\W))|(\+CMS ERROR: \d+($|\W))`, time.Second*60, func() error {
		return s.Port.Print(CMD_CTRL_Z)
	})
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

// Call to phone number
func (s *SIM900) Call(phoneNumber string, timeout time.Duration) (string, error) {
	s.PortMu.Lock()
	defer s.PortMu.Unlock()

	time.Sleep(1 * time.Second)
	re := regexp.MustCompile(`\^CEND:[,\d]+[^,\d]`)

	result := make(chan string, 1)
	defer s.Port.DelOutputListener(s.Port.AddOutputListener(func(bs []byte) {
		m := re.FindSubmatch(bs)
		if len(m) > 0 {
			result <- string(m[0])
		}
	}))
	err := s.Port.Println("ATD" + phoneNumber + ";")
	if err != nil {
		return "", err
	}

	select {
	case ret := <-result:
		return ret, nil
	case <-time.After(timeout):
		return "", errors.New("Wait call timeout")
	}
}

// WaitSMSText wait for sms match by phone number
func (s *SIM900) WaitSMSText(phoneNumber string, timeout time.Duration, inits ...func() error) (string, error) {
	result := make(chan string, 1)
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
	result := make(chan struct{}, 1)
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

	newMessagePattern := regexp.MustCompile(`\+CMT:[\s,\d]+\r?\n([a-zA-Z\d]+)(\r?\n|$)`)
	newCallPattern := regexp.MustCompile(`(^|\W)RING(\r?\n)+\+CLIP: "(\d+)"($|\W)`)
	endCallPattern := regexp.MustCompile(`\^CEND:\d+`)
	isRinging := atomic.Value{}
	isRinging.Store(false)

	s.Port.AddOutputListener(func(b []byte) {
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
				// msg, err := pdudecoder.Decode(bs)
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
				log.Println("Got message from:", msg.Address)
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
	})

	// initCommands := map[string]string{
	// 	"AT+CGMM":        CMD_OK,
	// 	"AT":             CMD_OK,
	// 	"AT+CMEE=1":      CMD_OK,
	// 	"ATE0":           CMD_OK,
	// 	"AT^HS=0,0":      CMD_OK,
	// 	"AT+CFUN?":       CMD_OK,
	// 	`AT+CLCK="SC",2`: CMD_OK,
	// 	"AT+CPIN?":       CMD_OK,
	// 	"AT^SYSINFO":     CMD_OK,
	// 	"AT+CLCC":        CMD_OK,
	// 	"AT+CREG=1":      CMD_OK,
	// 	"AT+CGREG=1":     CMD_OK,
	// 	"AT+CSSN=1,1":    CMD_OK,
	// 	"AT+CCWA=1":      CMD_OK,
	// 	"AT+CIMI":        CMD_OK,
	// 	"AT+CSQ":         CMD_OK,
	// 	"AT+COPS=3,0":    CMD_OK,
	// 	"AT+COPS?":       CMD_OK,
	// }
	// for c, r := range initCommands {
	// 	_, err := s.Wait4response(c, r, time.Second*5)
	// 	if err != nil {
	// 		log.Println(err)
	// 	}
	// 	time.Sleep(100 * time.Millisecond)
	// }

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
	// s.Wait4response("AT+COPS=0,0", CMD_OK, time.Second*5)

	// set pdu mode
	_, err = s.Wait4response("AT+CMGF=0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// Dont store sms, return pdu data
	_, err = s.Wait4response("AT+CNMI=1,2,0,0,0", CMD_OK, time.Second*5)
	// _, err = s.Wait4response("AT+CNMI=1,2,2,2,0", CMD_OK, time.Second*5)
	// _, err = s.Wait4response("AT+CNMI=3,2,0,0,0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}

	// set sms storage
	_, err = s.Wait4response(`AT+CPMS="ME","ME","ME"`, CMD_OK, time.Second*5)
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

func (s *SIM900) ClearSMS() error {
	_, err := s.Wait4response("AT+CMGD=1,4", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}
	return nil
}
