package main

import (
	"crypto/rand"
	"github.com/go-logr/logr"
	"io"
	"math/big"
	"net/http"
	"sync"
)

var mutex = sync.Mutex{}
var globalMap = map[string][]byte{}

// otp service example implementation.
// The example methods log the requests and return zero values.
type storekey struct {
	logger *logr.Logger
}

func (s *storekey) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		s.logger.Error(err, "failed to read request body", "address", request.RemoteAddr)
		writer.WriteHeader(500)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()
	otp, err := GenerateRandomString(20)
	if err != nil {
		s.logger.Error(err, "failed to generate OTP password", "address", request.RemoteAddr)
		writer.WriteHeader(500)
		return
	}
	globalMap[otp] = body
	_, err = writer.Write([]byte(otp))
	if err != nil {
		s.logger.Error(err, "failed to write http response", "address", request.RemoteAddr)
		writer.WriteHeader(500)
	} else {
		s.logger.Info("stored SSH key in OTP map", "address", request.RemoteAddr)
	}
}

// NewOtp returns the otp service implementation.
func NewStoreKey(logger *logr.Logger) *storekey {
	return &storekey{logger}
}

type otp struct {
	logger *logr.Logger
}

// NewOtp returns the otp service implementation.
func NewOtp(logger *logr.Logger) *otp {
	return &otp{logger}
}

func (s *otp) ServeHTTP(writer http.ResponseWriter, request *http.Request) {

	body, err := io.ReadAll(request.Body)
	if err != nil {
		s.logger.Error(err, "failed to read request body")
		writer.WriteHeader(500)
		return
	}
	mutex.Lock()
	defer mutex.Unlock()

	res, loaded := globalMap[string(body)]
	delete(globalMap, string(body))
	if !loaded {
		s.logger.Error(err, "no OTP found for provided SSH key", "address", request.RemoteAddr)
		writer.WriteHeader(400)
	} else {
		_, err := writer.Write(res)
		if err != nil {
			s.logger.Error(err, "failed to write http response", "address", request.RemoteAddr)
			writer.WriteHeader(500)
		} else {
			s.logger.Info("served one time password", "address", request.RemoteAddr)
		}
	}
}

// GenerateRandomString returns a securely generated random string.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func GenerateRandomString(n int) (string, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-"
	ret := make([]byte, n)
	for i := 0; i < n; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		ret[i] = letters[num.Int64()]
	}

	return string(ret), nil
}
