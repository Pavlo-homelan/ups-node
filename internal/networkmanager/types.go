package networkmanager

type Status struct {
	PortalMode    string  `json:"portal_mode"`
	ConnectedSSID *string `json:"connected_ssid"`
	Connection    *string `json:"connection_name"`
	IPAddress     *string `json:"ip_address"`
	State         string  `json:"state"`
}

type Network struct {
	SSID      string `json:"ssid"`
	Signal    uint8  `json:"signal"`
	Security  string `json:"security"`
	Connected bool   `json:"connected"`
}

type ScanResult struct {
	Networks []Network `json:"networks"`
}

type ConnectResult struct {
	Message string `json:"message"`
}
