package main

import (
	"crypto/rand" // generate a secure random salt
	"encoding/hex"
	//	"encoding/hex"
	"errors"
	"fmt"
	"strings"
"crypto/subtle"
	"golang.org/x/crypto/argon2" // proved the argon2 provides the password-hashing algorizam
)

const (
	memory      = 64 * 1024
	iterations  = 3
	parallelism = 2
	saltLength  = 16
	keyLength   = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength) // fills Ower 16-byte slice with cryptographically secure random bytes.
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey(
		[]byte(password), // -> Password  convert to bytes
		salt,             // --> the random 16 byte was generated
		iterations,       //--> 3 hashing passes
		memory,           // --> 2 processing lines
		parallelism,      // --> 2 processing lanes
		keyLength,        // a 32-byte output hasg
	)
	return fmt.Sprintf("%x.%x", salt, hash), nil
}
func CheckPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, ".")
	if len(parts) != 2 {
		return false, errors.New("invalid has format")
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false, err
	}
  // Decode the saved DencodedHash
  expectedHash,err:=hex.DecodeString(parts[1])
  if err!=nil{
  return false ,err
  }
  actualHash:=argon2.IDKey(
 []byte(password),
  salt,
  iterations,
  memory,
  parallelism,
  uint32(len(expectedHash)),
  )
  return subtle.ConstantTimeCompare(actualHash,expectedHash)==1,nil
}
