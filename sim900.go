package sim900

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/vinhjaxt/serial"
)

/*******************************************************************************************
********************************	TYPE DEFINITIONS	************************************
*******************************************************************************************/

// A SIM900 is the representation of a SIM900 GSM modem with several utility features.
type SIM900 struct {
	Port   *serial.SerialPort
	logger *log.Logger
}

/*******************************************************************************************
********************************   GSM: BASIC FUNCTIONS  ***********************************
*******************************************************************************************/

// New creates and initializes a new SIM900 device.
func New() *SIM900 {
	return &SIM900{
		Port:   serial.New(),
		logger: log.New(os.Stdout, "[sim900] ", log.LstdFlags),
	}
}

// Connect creates a connection with the SIM900 modem via serial port and test communications.
func (s *SIM900) Connect(port string, baud int) error {
	// Open device serial port
	if err := s.Port.Open(port, baud, time.Millisecond*100); err != nil {
		return err
	}
	// Ping to Modem
	return s.Ping()
}

func (sim *SIM900) Disconnect() error {
	// Close device serial port
	return sim.Port.Close()
}

func (sim *SIM900) wait4response(cmd, expected string, timeout time.Duration) ([]string, error) {
	// Send command
	if cmd != "" {
		if err := sim.Port.Println(cmd); err != nil {
			return nil, err
		}
	}
	// Wait for command response
	regexp := expected + "|" + CMD_ERROR
	response, err := sim.Port.WaitForRegexTimeout(regexp, timeout)
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

// SendSMS return sms id or error
func (s *SIM900) SendSMS(number, msg string) (string, error) {
	mode, err := s.GetSMSMode()
	if err != nil {
		return "", err
	}
	defer func() {
		s.SetSMSMode(mode)
	}()
	// Set message format
	if err := s.SetSMSMode(TEXT_MODE); err != nil {
		return "", err
	}
	// Send command
	cmd := fmt.Sprintf(CMD_CMGS, number)
	if err := s.Port.Println(cmd); err != nil {
		return "", err
	}
	// Wait modem to be ready
	time.Sleep(time.Second * 1)
	// Send message
	response, err := s.wait4response(msg+CMD_CTRL_Z, `(\+CMGS: (\d+)($|\W))|(\+CMS ERROR: \d+($|\W))`, time.Second*60)
	if err != nil {
		return "", err
	}
	// Message sent succesfully
	return response[2], nil
}

// WaitSMS will return when either a new SMS is recived or the timeout has expired.
// The returned value is the memory ID of the received SMS, use ReadSMS to read SMS content.
func (s *SIM900) WaitSMS(timeout time.Duration) (string, error) {
	_, err := s.wait4response("AT+CNMI=1,1,0,0,0", CMD_OK, 5*time.Second)
	if err != nil {
		return "", err
	}
	response, err := s.wait4response("", CMD_CMTI_REGEXP, timeout)
	if err != nil {
		return "", err
	}
	return response[2], nil
}

// ReadSMS retrieves SMS text from inbox memory by ID.
func (s *SIM900) ReadSMS(id, mode string) ([]string, error) {
	modeBak, err := s.GetSMSMode()
	if err != nil {
		return nil, err
	}
	defer func() {
		s.SetSMSMode(modeBak)
	}()
	// Set message format
	if err := s.SetSMSMode(mode); err != nil {
		return nil, err
	}
	// Send command
	cmd := fmt.Sprintf(CMD_CMGR, id)
	body, err := s.wait4response(cmd, CMD_CMGR_REGEXP+"|(\\+CMS ERROR: \\d+)", time.Second*5)
	if err != nil {
		return nil, err
	}
	// Reading succesful get message data
	return body[1:], nil
}

// DeleteSMS deletes SMS from inbox memory by ID.
func (s *SIM900) DeleteSMS(id string) error {
	// Send command
	cmd := fmt.Sprintf(CMD_CMGD, id)
	_, err := s.wait4response(cmd, CMD_OK, time.Second*1)
	return err
}

// ClearSMS deletes SMS from inbox memory by ID.
func (s *SIM900) ClearSMS() error {
	// Send command
	_, err := s.wait4response("AT+CMGD=1,4", CMD_OK, time.Second*1)
	return err
}

// Ping modem
func (s *SIM900) Ping() error {
	time.Sleep(1 * time.Second)
	_, err := s.wait4response("ATE0", CMD_OK, time.Second*5)
	if err != nil {
		return err
	}
	_, err = s.wait4response(CMD_AT, CMD_OK, time.Second*5)
	return err
}

// SetSMSMode selects SMS Message Format ("0" = PDU mode, "1" = Text mode)
func (s *SIM900) SetSMSMode(mode string) error {
	cmd := fmt.Sprintf(CMD_CMGF_SET, mode)
	_, err := s.wait4response(cmd, CMD_OK, time.Second*1)
	return err
}

// SetSMSMode reads SMS Message Format (0 = PDU mode, 1 = Text mode)
func (s *SIM900) GetSMSMode() (string, error) {
	response, err := s.wait4response(CMD_CMGF, CMD_CMGF_REGEXP, time.Second*1)
	if err != nil {
		return "", err
	}
	return response[2], nil
}
