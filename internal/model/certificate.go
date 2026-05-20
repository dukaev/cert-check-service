package model

import "time"

type Certificate struct {
	Serial    string
	NotBefore time.Time
	NotAfter  time.Time
	RevokedAt *time.Time
}
