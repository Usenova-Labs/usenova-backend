package client

type ListResponse struct {
	Running  int           `json:"running"`
	Services []interface{} `json:"services"`
}
