package api

type DefaultJSONResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}
