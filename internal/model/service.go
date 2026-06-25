package model

import "time"

const (
	ServiceName      = "webitel-kb"
	ServiceNamespace = "webitel"
)

var (
	Version        = "0.0.0"
	Commit         = "hash"
	CommitDate     = time.Now().String()
	Branch         = "branch"
	BuildTimestamp = ""
)
