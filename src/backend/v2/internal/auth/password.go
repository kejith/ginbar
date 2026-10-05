package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordBytes = 12
	MaxPasswordBytes = 1024

	minArgonMemoryKiB   uint32 = 8 * 1024
	maxArgonMemoryKiB   uint32 = 256 * 1024
	maxArgonIterations  uint32 = 10
	maxArgonParallelism uint8  = 8
	minSaltBytes               = 16
	maxSaltBytes               = 64
	minKeyBytes                = 16
	maxKeyBytes                = 64
)

var (
	ErrInvalidPassword   = errors.New("password does not satisfy input bounds")
	ErrInvalidParameters = errors.New("invalid password hashing parameters")
	ErrMalformedVerifier = errors.New("malformed password verifier")
)

type PasswordParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	KeyBytes    uint32
}

func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		MemoryKiB:   64 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltBytes:   16,
		KeyBytes:    32,
	}
}

func HashPassword(password string, params PasswordParams) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	if err := validateParams(params); err != nil {
		return "", err
	}

	salt := make([]byte, params.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.MemoryKiB,
		params.Parallelism,
		params.KeyBytes,
	)
	return encodeVerifier(params, salt, key), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	if err := validatePassword(password); err != nil {
		return false, err
	}
	params, salt, expected, err := parseVerifier(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.MemoryKiB,
		params.Parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func validatePassword(password string) error {
	if len(password) < MinPasswordBytes || len(password) > MaxPasswordBytes {
		return ErrInvalidPassword
	}
	return nil
}

func validateParams(params PasswordParams) error {
	if params.MemoryKiB < minArgonMemoryKiB || params.MemoryKiB > maxArgonMemoryKiB ||
		params.Iterations < 1 || params.Iterations > maxArgonIterations ||
		params.Parallelism < 1 || params.Parallelism > maxArgonParallelism ||
		params.SaltBytes < minSaltBytes || params.SaltBytes > maxSaltBytes ||
		params.KeyBytes < minKeyBytes || params.KeyBytes > maxKeyBytes {
		return ErrInvalidParameters
	}
	return nil
}

func encodeVerifier(params PasswordParams, salt, key []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.MemoryKiB,
		params.Iterations,
		params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func parseVerifier(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}

	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}
	memory, ok := parseUintField(fields[0], "m=", 32)
	if !ok {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}
	iterations, ok := parseUintField(fields[1], "t=", 32)
	if !ok {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}
	parallelism, ok := parseUintField(fields[2], "p=", 8)
	if !ok {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}

	params := PasswordParams{
		MemoryKiB:   uint32(memory),
		Iterations:  uint32(iterations),
		Parallelism: uint8(parallelism),
		SaltBytes:   uint32(len(salt)),
		KeyBytes:    uint32(len(key)),
	}
	if err := validateParams(params); err != nil {
		return PasswordParams{}, nil, nil, ErrMalformedVerifier
	}
	return params, salt, key, nil
}

func parseUintField(value, prefix string, bitSize int) (uint64, bool) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value[len(prefix):], 10, bitSize)
	return parsed, err == nil
}
