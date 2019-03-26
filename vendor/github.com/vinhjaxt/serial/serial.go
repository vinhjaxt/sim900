package serial

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

const rxDataTimeout = 20 * time.Millisecond

type SerialPort struct {
	fileLog      *log.Logger
	TxMu         *sync.Mutex
	RxMu         *sync.Mutex
	NeedRx       int32
	Port         io.ReadWriteCloser
	Opened       int32
	Verbose      bool
	eventMap     map[uint32]func([]byte)
	eventNextID  uint32
	eventMapLock *sync.RWMutex
	rxChar       chan byte
	rxData       chan string
	rxTimer      <-chan time.Time
}

// New create new instance
func New() *SerialPort {
	return &SerialPort{
		Verbose:      true,
		eventMap:     map[uint32]func([]byte){},
		eventMapLock: &sync.RWMutex{},
		eventNextID:  0,
		Opened:       0,
		TxMu:         &sync.Mutex{},
		RxMu:         &sync.Mutex{},
		fileLog:      nil,
	}
}

// Open open connection to port
func (sp *SerialPort) Open(name string, baud int, timeout ...time.Duration) error {
	if atomic.LoadInt32(&sp.Opened) == 1 {
		return errors.New(name + " already opened")
	}
	var readTimeout time.Duration
	if len(timeout) > 0 {
		readTimeout = timeout[0]
	}

	comPort, err := openPort(name, baud, readTimeout)
	if err != nil {
		return errors.New(name + ": " + err.Error())
	}

	atomic.StoreInt32(&sp.Opened, 1)
	if sp.Verbose == false {
		file, err := os.OpenFile(fmt.Sprintf("log_serial_%d.txt", time.Now().Unix()), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.Println(err)
		}
		sp.fileLog = log.New(file, "PREFIX: ", log.Ldate|log.Ltime)
		sp.fileLog.SetPrefix(fmt.Sprintf("[%s] ", name))
	}

	sp.Port = comPort
	sp.rxChar = make(chan byte, 512)
	sp.rxData = make(chan string, 16)

	go sp.readSerialPort()
	go sp.processSerialPort()
	sp.log("Port %s@%d opened", name, baud)
	return nil
}

// Close close the current Serial Port.
func (sp *SerialPort) Close() error {
	sp.Println("\x1A")
	if atomic.LoadInt32(&sp.Opened) == 1 {
		atomic.StoreInt32(&sp.Opened, 0)
		close(sp.rxChar)
		close(sp.rxData)
		sp.log("Port closed")
		return sp.Port.Close()
	}
	return nil
}

// This method prints data trough the serial port.
func (sp *SerialPort) Write(data []byte) (n int, err error) {
	if atomic.LoadInt32(&sp.Opened) == 1 {
		sp.TxMu.Lock()
		n, err = sp.Port.Write(data)
		sp.TxMu.Unlock()
		if err != nil {
			// Do nothing
		} else {
			sp.log("Tx >> %s", string(data))
		}
	} else {
		err = errors.New("Port is not opened")
	}
	return
}

// Print send data to port
func (sp *SerialPort) Print(str string) error {
	if atomic.LoadInt32(&sp.Opened) == 1 {
		sp.TxMu.Lock()
		_, err := sp.Port.Write([]byte(str))
		sp.TxMu.Unlock()
		if err != nil {
			return err
		} else {
			sp.log("Tx >> %s", str)
		}
	} else {
		return errors.New("Port is not opened")
	}
	return nil
}

// Println prints data to the serial port as human-readable ASCII text followed by a carriage return character
// (ASCII 13, CR, '\r') and a newline character (ASCII 10, LF, '\n').
func (sp *SerialPort) Println(str string) error {
	return sp.Print(str + "\r\n")
}

// Printf formats according to a format specifier and print data trough the serial port.
func (sp *SerialPort) Printf(format string, args ...interface{}) error {
	str := format
	if len(args) > 0 {
		str = fmt.Sprintf(format, args...)
	}
	return sp.Print(str)
}

// SendFile send a binary file trough the serial port. If EnableLog is active then this method will log file related data.
func (sp *SerialPort) SendFile(filepath string) error {
	sentBytes := 0
	q := 512
	data := []byte{}
	file, err := ioutil.ReadFile(filepath)
	if err != nil {
		return err
	} else {
		fileSize := len(file)
		sp.TxMu.Lock()
		defer sp.TxMu.Unlock()
		for sentBytes <= fileSize {
			if len(file[sentBytes:]) > q {
				data = file[sentBytes:(sentBytes + q)]
			} else {
				data = file[sentBytes:]
			}
			_, err := sp.Port.Write(data)
			if err != nil {
				return err
			} else {
				sentBytes += q
				time.Sleep(time.Millisecond * 100)
			}
		}
	}
	return nil
}

// WaitForRegexTimeout wait for a defined regular expression for a defined amount of time.
func (sp *SerialPort) WaitForRegexTimeout(cmd, exp string, timeout time.Duration, inits ...func() error) ([]string, error) {
	if atomic.LoadInt32(&sp.Opened) == 1 {
		timeExpired := false
		regExpPattern := regexp.MustCompile(exp)
		c1 := make(chan []string, 1)
		sp.RxMu.Lock()
		atomic.StoreInt32(&sp.NeedRx, 1)
		go func() {
			sp.log(">> Waiting: \"%s\"", exp)
			result := []string{}
			lines := ""
			for !timeExpired {
				lines += <-sp.rxData
				result = regExpPattern.FindStringSubmatch(lines)
				if len(result) > 0 {
					lines = ""
					c1 <- result
					break
				}
			}
			atomic.StoreInt32(&sp.NeedRx, 0)
			sp.RxMu.Unlock()
		}()

		if cmd != "" {
			if err := sp.Println(cmd); err != nil {
				return nil, err
			}
		}

		for _, fn := range inits {
			err := fn()
			if err != nil {
				return nil, err
			}
		}

		select {
		case data := <-c1:
			sp.log(">> Matched: %q", data[0])
			return data, nil
		case <-time.After(timeout):
			timeExpired = true
			sp.log(">> Failed: \"%s\"", exp)
			return nil, fmt.Errorf("Timeout \"%s\"", exp)
		}
	} else {
		return nil, errors.New("Port is not opened")
	}
}

func (sp *SerialPort) readSerialPort() {
	rxBuff := make([]byte, 256)
	for atomic.LoadInt32(&sp.Opened) == 1 {
		n, _ := sp.Port.Read(rxBuff)
		for _, b := range rxBuff[:n] {
			if atomic.LoadInt32(&sp.Opened) == 1 {
				sp.rxChar <- b
			} else {
				break
			}
		}
	}
}

// DelOutputListener remove func from map
func (sp *SerialPort) DelOutputListener(id uint32) {
	sp.eventMapLock.Lock()
	defer sp.eventMapLock.Unlock()
	delete(sp.eventMap, id)
}

// AddOutputListener add func to capture ouput
func (sp *SerialPort) AddOutputListener(fn func([]byte)) uint32 {
	sp.eventMapLock.Lock()
	defer sp.eventMapLock.Unlock()
	id := atomic.AddUint32(&sp.eventNextID, 1)
	sp.eventMap[id] = fn
	return id
}

func (sp *SerialPort) processSerialPort() {
	defer func() {
		recover()
	}()
	var screenBuff []byte
	var lastRxByte byte
	for {
		if atomic.LoadInt32(&sp.Opened) == 1 {
			select {
			case lastRxByte = <-sp.rxChar:
				screenBuff = append(screenBuff, lastRxByte)
				sp.rxTimer = time.After(rxDataTimeout)
				break
			case <-sp.rxTimer:
				if screenBuff == nil {
					break
				}
				sp.eventMapLock.RLock()
				for _, fn := range sp.eventMap {
					sp.eventMapLock.RUnlock()
					go fn(screenBuff)
					sp.eventMapLock.Lock()
				}
				sp.eventMapLock.RUnlock()
				sp.log("Rx << %q", screenBuff)
				if atomic.LoadInt32(&sp.NeedRx) == 1 {
					sp.rxData <- string(screenBuff)
				}
				screenBuff = nil //Clean buffer
				break
			}
		} else {
			break
		}
	}
}

func (sp *SerialPort) log(format string, a ...interface{}) {
	if sp.Verbose {
		log.Printf(format, a...)
	} else if sp.fileLog != nil {
		sp.fileLog.Printf(format, a...)
	}
}
