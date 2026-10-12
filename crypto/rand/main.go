package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func GeneratePairingCode() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}