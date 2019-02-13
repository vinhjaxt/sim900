package sim900

// AT commands
const (
	CMD_AT               string = "AT"
	CMD_OK               string = `(^|\W)OK($|\W)`
	CMD_ERROR            string = `(^|\W)ERROR($|\W)`
	CMD_CMGF             string = "AT+CMGF?"
	CMD_CMGF_SET         string = "AT+CMGF=%s"
	CMD_CMGF_REGEXP      string = `(\+CMGF: (\d+)($|\W))`
	CMD_CMGF_RX          string = "+CMGF: "
	CMD_CTRL_Z           string = "\x1A"
	CMD_CMGS             string = "AT+CMGS=\"%s\""
	CMD_CMGD             string = "AT+CMGD=%s"
	CMD_CMGR             string = "AT+CMGR=%s"
	CMD_CMGR_TEXT_REGEXP string = `(\+CMGR: (.+)\r\n?([\s\S]+)\r\n?(OK|\^BOOT)($|\W))`
	CMD_CMGR_PDU_REGEXP  string = `(\+CMGR: (.+)\r\n?(\w+)(\r\n?|$|\W))`
	CMD_CMGR_RX          string = "+CMGR: "
	CMD_CMTI_REGEXP      string = `(\+CMTI: "SM",(\d+)($|\W))`
	CMD_CMTI_RX          string = "+CMTI: \"SM\","
)

// SMS Message Format
const (
	PDU_MODE  string = "0"
	TEXT_MODE string = "1"
)
