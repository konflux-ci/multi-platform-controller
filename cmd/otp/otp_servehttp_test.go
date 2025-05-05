package main

import (
	"bytes"
	"crypto"
	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
	"github.com/pkg/errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

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

		JustBeforeEach(func() {
			testOtp = NewOtp(&logCapture.Logger)
		})

		Context("with valid SSH key (otp)", func() {

			It("serves a one-time password when given a valid key", func() {
				// stores the valid ssh key in globalMap
				store := NewStoreKey(&logCapture.Logger)
				storeReq := httptest.NewRequest("POST", "/store", strings.NewReader(rsaKey))
				store.ServeHTTP(rr, storeReq)
				storedKey := rr.Body.String()
				GinkgoWriter.Printf("Stored key used for OTP: %q\n", storedKey)

				req = httptest.NewRequest("POST", "/otp", strings.NewReader(storedKey))
				rr = httptest.NewRecorder()
				testOtp.ServeHTTP(rr, req)

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

		JustBeforeEach(func() {
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
				func(input string, label string) {
					//input := inputFn()
					GinkgoWriter.Printf("Running test for: %s\n", label)
					GinkgoWriter.Printf("Valid key value is: %s\n", input)

					req := httptest.NewRequest("POST", "/store", strings.NewReader(input))
					testStorekey.ServeHTTP(rr, req)

					Expect(rr.Code).To(Equal(http.StatusOK))
					Expect(rr.Body.String()).NotTo(BeEmpty())
					Expect(logCapture.Contains("stored SSH key in OTP map")).To(BeTrue(), "Log should indicate success of SSH key placement in globalMap")
				},

				Entry("key with trailing whitespace", rsaKey+"\n\t", "rsa key with whitespace at the end"),
				Entry("key with comment", rsaKey+" user@example.com", "rsa key with comment at the end"),
				Entry("very long key", rsaKey+rsaKey+rsaKey, "a very long rsa key"),
				Entry("ed25519 key", edKey, "an ed25519 key"),
			)
		})

		// Skipping while KFLUXINFRA-1569 in SSH key validation is being fixed
		PContext("with invalid ssh key (storekey)", func() {
			badPrefixKey := "ssh-xyz123 AAAAB3NzaC1yc2EAAAADAQABAAABAQCe72TIPRwW/tDWPbNdJg3a6Rqy1/9kM002NLCx82fAN8GTvUj6VAD4Nl9om5BT7o0tfCcYgpDmTDrl7QPhBGd5ew0VKHO8o++SwhG6QI2mR+867qIdXP1B1ZdxO8eYndIn+ssOZcmbp4XxrI5/xWSNAU2XMckuSeUFZRTDNUVoKYmDMgnZL+BV6eQKO4jJmtuctDku4yb7YjcJYw7L+LOU7fBnsEpzvPJ/P9pGckpVI5nYIdQIUBxmVloa6BiKCGbu4yzPZ2zWISPODyvv6cmKk3s+YGild/TrVC+aRRN4TpiYq5NVT0lkkHpzhShVPukOR4xXoqZQLWYwAPLuUIBn"
			DescribeTable("returns 500 for invalid SSH keys (storekey)",
				func(input string, label string) {
					GinkgoWriter.Printf("Running test for: %s\n", label)
					GinkgoWriter.Printf("Invalid key value is: `%s`\n", input)

					req := httptest.NewRequest("POST", "/store", strings.NewReader(input))

					testStorekey.ServeHTTP(rr, req)
					Expect(rr.Code).To(Equal(http.StatusInternalServerError))
					Expect(logCapture.Contains("failed to read request body")).To(BeTrue(), "Log should indicate a problem with the request's body")
				},

				Entry("empty string", "", "An empty string"),
				Entry("non-ssh string", "not-an-SSH-key", "A non-ssh key string"),
				Entry("bad prefix", badPrefixKey, "An ssh key string with a badly formed prefix"),
				Entry("missing payload", "ssh-rsa AAAAB3Nz", "An ssh key string with truncated content"),
			)
		})
	})
})

// Generates a real SSH public key string, with option to change the encryption algorithm. Can be broadened to more
// algorithms such as diffie-hellman, if we ever choose to test it.
func generateValidSSHKey(keyType string) (string, error) {
	switch keyType {
	case "rsa":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", err
		}
		pub, err := ssh.NewPublicKey(&key.PublicKey)
		if err != nil {
			return "", err
		}
		return string(ssh.MarshalAuthorizedKey(pub)), nil

	case "ed25519":
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
