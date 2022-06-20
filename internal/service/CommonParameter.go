package service

type CommonParameter struct {
	Authorization string `json:"Authorization" :"authorization"`
	Version       string `json:"version" :"version"`
	Uuid          string `json:"uuid" :"uuid"`
	DeviceType    string `json:"deviceType" :"device_type"`
	DeviceBrand   string `json:"deviceBrand" :"device_brand"`
	DeviceVersion string `json:"deviceVersion" :"device_version"`
	Lange         string `json:"lange" :"lange"`
	TimeZone      string `json:"timeZone" :"time_zone"`
	Sign          string `json:"sign" :"sign"`
}
