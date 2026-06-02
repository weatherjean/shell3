package web

import _ "embed"

//go:embed assets/index.html
var indexHTML []byte

//go:embed assets/login.html
var loginHTML []byte
