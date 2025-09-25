// Assisted-by: Gemini
package main

import (
	"bytes"
	"crypto"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
	"github.com/pkg/errors"

	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/crypto/ssh"
)

// A unit test for storekey.ServeHTTP and otp.ServeHTTP.
// Tests happy and sad paths for each, each for their own specific functionality:
//  1. otp.ServeHTTP checks it can provision a one time password properly when given an ssh key, and checks that if
//     globalMap does not have a matching otp value, it returns the correct error and log writes
//  2. storekey.ServeHTTP checks it can store a valid ssh key in globalMap, that it can store borderline-but-valid ssh
//     keys in globalMap, and that it can validate an ssh key's structure to make sure it's indeed an ssh key and not
//     some garbled/truncated/invalid content
//  3. end-to-end test to make sure two keys don't return the same otp and the two otps don't return the same key
const (
	rsaKeyType     string = "rsa"
	ed25519KeyType string = "ed25519"
)

var _ = Describe("ServeHTTP handlers", Serial, func() {
	var (
		rr         *httptest.ResponseRecorder
		rsaKey     string
		req        *http.Request
		err        error
		logCapture *LogCapture
		edKey      string
	)

	BeforeEach(func() {
		rr = httptest.NewRecorder()
		logCapture = NewLogCapture()
		rsaKey, err = generateValidSSHKey("rsa")
		Expect(err).ToNot(HaveOccurred())
		edKey, err = generateValidSSHKey("ed25519")
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Testing otp.ServeHTTP", func() {
		var testOtp *otp

		BeforeEach(func() {
			testOtp = NewOtp(&logCapture.Logger)
		})

		Context("with valid SSH key (otp)", func() {

			It("serves a one-time password", func() {
				By("Initializing a new store with a valid ssh key")
				store := NewStoreKey(&logCapture.Logger)
				storedKey := storeKeyAndGetOTP(store, rsaKey)
				GinkgoWriter.Printf("Stored key used for OTP: %q\n", storedKey)

				By("Requiring an OTP for that ssh key")
				req = httptest.NewRequest("POST", "/otp", strings.NewReader(storedKey))
				rr = httptest.NewRecorder()
				testOtp.ServeHTTP(rr, req)

				By("Verifying that the OTP is returned successfully")
				Expect(rr.Code).To(Equal(http.StatusOK))
				Expect(rr.Body.String()).NotTo(BeEmpty())
				Expect(logCapture.Contains("served one time password")).To(BeTrue(), "Log should indicate success of one time password provision")
			})
		})

		Context("when the key is valid but missing from the store", func() {
			It("returns 400 and logs a missing key error", func() {
				req = httptest.NewRequest("POST", "/otp", strings.NewReader("doesn't really matter, does it?"))
				testOtp.ServeHTTP(rr, req)

				Expect(rr.Code).To(Equal(http.StatusBadRequest))
				Expect(logCapture.Contains("no OTP found for provided SSH key")).To(BeTrue(),
					"Log should indicate the SSH key wasn't found in the store")
			})
		})
	})

	Describe("Testing storekey.ServeHTTP", func() {
		var testStorekey *storekey

		BeforeEach(func() {
			testStorekey = NewStoreKey(&logCapture.Logger)
		})

		Context("with valid SSH key (storekey)", func() {

			It("completely valid SSH key - stores the SSH key in OTP map when given a valid key", func() {
				req = httptest.NewRequest("POST", "/store", strings.NewReader(rsaKey))
				testStorekey.ServeHTTP(rr, req)

				Expect(rr.Code).To(Equal(http.StatusOK))
				Expect(rr.Body.String()).NotTo(BeEmpty())
				Expect(logCapture.Contains("stored SSH key in OTP map")).To(BeTrue(), "Log should indicate success of SSH key placement in globalMap")
			})

			DescribeTable("valid-but-borderline SSH keys - stores the SSH key in OTP map when given a valid key",
				func(inputFn func() string, label string) {
					input := inputFn()
					GinkgoWriter.Printf("Running test for: %s\n", label)
					GinkgoWriter.Printf("Valid key value is: %s\n", input)

					req := httptest.NewRequest("POST", "/store", strings.NewReader(input))
					testStorekey.ServeHTTP(rr, req)

					Expect(rr.Code).To(Equal(http.StatusOK))
					Expect(rr.Body.String()).NotTo(BeEmpty())
					Expect(logCapture.Contains("stored SSH key in OTP map")).To(BeTrue(), "Log should indicate success of SSH key placement in globalMap")
				},

				Entry("key with trailing whitespace", func() string { return rsaKey + "\n\t" }, "rsa key with whitespace at the end"),
				Entry("key with comment", func() string { return rsaKey + " user@example.com" }, "rsa key with comment at the end"),
				Entry("very long key", func() string { return rsaKey + rsaKey + rsaKey }, "a very long rsa key"),
				Entry("ed25519 key", func() string { return edKey }, "an ed25519 key"),
			)
		})
	})

	Describe("End-to-end otp flow using ServeHTTP", func() {
		It("returns distinct otps for different keys and maps each back to the correct key", func() {
			store := NewStoreKey(&logCapture.Logger)
			testOtp := NewOtp(&logCapture.Logger)

			By("Storing an rsa key and an ed25519 key and getting their otps, Expecting each to be different than " +
				"the other")
			rsaOTP := storeKeyAndGetOTP(store, rsaKey)
			edOTP := storeKeyAndGetOTP(store, edKey)
			Expect(rsaOTP).NotTo(Equal(edOTP), "Each stored key should result in a distinct OTP")

			By("Verifying the otp created from storing the rsa key returns the rsa key")
			rr := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/otp", strings.NewReader(rsaOTP))
			testOtp.ServeHTTP(rr, req)
			Expect(rr.Code).To(Equal(http.StatusOK))
			Expect(rr.Body.String()).To(Equal(rsaKey), "OTP should return original rsa key")

			By("Verifying the otp created from storing the ed25519 key returns the ed25519 key")
			rr = httptest.NewRecorder()
			req = httptest.NewRequest("POST", "/otp", strings.NewReader(edOTP))
			testOtp.ServeHTTP(rr, req)
			Expect(rr.Code).To(Equal(http.StatusOK))
			Expect(rr.Body.String()).To(Equal(edKey), "OTP should return original ed25519 key")
		})
	})
})

// helper method to DRY things up a bit
func storeKeyAndGetOTP(handler http.Handler, key string) string {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/store", strings.NewReader(key))
	handler.ServeHTTP(rr, req)

	Expect(rr.Code).To(Equal(http.StatusOK), "Failed to store SSH key")
	otp := strings.TrimSpace(rr.Body.String())
	Expect(otp).NotTo(BeEmpty(), "OTP returned for SSH key was empty")

	return otp
}

// Generates a real SSH public key string, with option to change the encryption algorithm. Can be broadened to more
// algorithms such as diffie-hellman, if we ever choose to test it.
func generateValidSSHKey(keyType string) (string, error) {
	switch keyType {
	case rsaKeyType:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", err
		}
		pub, err := ssh.NewPublicKey(&key.PublicKey)
		if err != nil {
			return "", err
		}
		return string(ssh.MarshalAuthorizedKey(pub)), nil

	case ed25519KeyType:
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", err
		}
		sshPub, err := ssh.NewPublicKey(crypto.PublicKey(pub))
		if err != nil {
			return "", err
		}
		return string(ssh.MarshalAuthorizedKey(sshPub)), nil

	default:
		return "", errors.New("unsupported key type")
	}
}

// A helper to search for substrings in logs
func (lc *LogCapture) Contains(substr string) bool {
	return strings.Contains(lc.Buffer.String(), substr)
}

// Util for grabbing log writes and testing that the ServeHTTPs write what they need to write
type LogCapture struct {
	Buffer *bytes.Buffer
	Logger logr.Logger
}

// The log sink itself
func NewLogCapture() *LogCapture {
	buf := &bytes.Buffer{}
	stdLog := log.New(buf, "", 0)
	logger := stdr.New(stdLog)

	return &LogCapture{buf, logger}
}
