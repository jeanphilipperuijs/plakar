package encryption

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"hash"
	"io"

	"github.com/PlakarKorp/plakar/hashing"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

const (
	chunkSize   = 64 * 1024 // Size of each chunk for encryption/decryption
	DEFAULT_KDF = "SCRYPT"
)

type Configuration struct {
	KDFParams KDFParams
	Algorithm string
	Canary    []byte
}

type KDFParams struct {
	KDF          string
	Salt         []byte
	Argon2Params *Argon2Params `msgpack:"argon2,omitempty"`
	ScryptParams *ScryptParams `msgpack:"scrypt,omitempty"`
	Pbkdf2Params *PBKDF2Params `msgpack:"pbkdf2,omitempty"`
}

func NewDefaultKDFParams(KDF string) (*KDFParams, error) {
	saltSize := uint32(16)
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	switch KDF {
	case "ARGON2":
		return &KDFParams{
			KDF:  "ARGON2",
			Salt: salt,
			Argon2Params: &Argon2Params{
				SaltSize: saltSize,
				Time:     1,
				Memory:   64 * 1024,
				Thread:   4,
				KeyLen:   32,
			},
		}, nil
	case "SCRYPT":
		return &KDFParams{
			KDF:  "SCRYPT",
			Salt: salt,
			ScryptParams: &ScryptParams{
				SaltSize: saltSize,
				N:        1 << 15,
				R:        8,
				P:        1,
				KeyLen:   32,
			},
		}, nil
	case "PBKDF2":
		return &KDFParams{
			KDF:  "PBKDF2",
			Salt: salt,
			Pbkdf2Params: &PBKDF2Params{
				SaltSize:   saltSize,
				Iterations: 100000,
				KeyLen:     32,
				Hashing:    "SHA256",
			},
		}, nil
	}
	return nil, fmt.Errorf("unsupported KDF: %s", KDF)
}

type Argon2Params struct {
	SaltSize uint32
	Time     uint32
	Memory   uint32
	Thread   uint8
	KeyLen   uint32
}

type ScryptParams struct {
	SaltSize uint32
	N        int
	R        int
	P        int
	KeyLen   int
}

type PBKDF2Params struct {
	SaltSize   uint32
	Iterations int
	KeyLen     int
	Hashing    string
}

func NewDefaultConfiguration() *Configuration {
	kdfParams, err := NewDefaultKDFParams(DEFAULT_KDF)
	if err != nil {
		panic(err)
	}

	return &Configuration{
		Algorithm: "AES256-GCM",
		KDFParams: *kdfParams,
	}
}

func Salt() (salt []byte, err error) {
	_, err = rand.Read(salt[:])
	return
}

// DeriveKey generates a secret from a passphrase using KDF parameters
func DeriveKey(params KDFParams, passphrase []byte) ([]byte, error) {
	switch params.KDF {
	case "ARGON2":
		return argon2.Key(passphrase, params.Salt[:], params.Argon2Params.Time, params.Argon2Params.Memory, params.Argon2Params.Thread, params.Argon2Params.KeyLen), nil
	case "SCRYPT":
		return scrypt.Key(passphrase, params.Salt[:], params.ScryptParams.N, params.ScryptParams.R, params.ScryptParams.P, params.ScryptParams.KeyLen)
	case "PBKDF2":
		return pbkdf2.Key(passphrase, params.Salt[:], params.Pbkdf2Params.Iterations, params.Pbkdf2Params.KeyLen, func() hash.Hash { return hashing.GetHasher(params.Pbkdf2Params.Hashing) }), nil
	}
	return nil, fmt.Errorf("unsupported KDF: %s", params.KDF)
}

func DeriveCanary(key []byte) ([]byte, error) {
	canary := make([]byte, 32)
	if _, err := rand.Read(canary); err != nil {
		return nil, err
	}

	rd, err := EncryptStream(key, bytes.NewReader(canary))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(rd)
}

func VerifyCanary(key []byte, canary []byte) bool {
	rd, err := DecryptStream(key, bytes.NewReader(canary))
	if err != nil {
		return false
	}
	_, err = io.ReadAll(rd)
	return err == nil
}

// EncryptStream encrypts a stream using AES-GCM with a random session-specific subkey
func EncryptStream(key []byte, r io.Reader) (io.Reader, error) {
	// Generate a random subkey for data encryption
	subkey := make([]byte, 32)
	if _, err := rand.Read(subkey); err != nil {
		return nil, err
	}

	// Encrypt the subkey with the main key using AES-GCM
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Generate a nonce for subkey encryption
	subkeyNonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(subkeyNonce); err != nil {
		return nil, err
	}

	// Encrypt the subkey
	encSubkey := gcm.Seal(nil, subkeyNonce, subkey, nil)

	// Set up AES-GCM for data encryption using the subkey
	dataBlock, err := aes.NewCipher(subkey)
	if err != nil {
		return nil, err
	}
	dataGCM, err := cipher.NewGCM(dataBlock)
	if err != nil {
		return nil, err
	}

	// Set up the pipe for streaming encryption
	pr, pw := io.Pipe()

	// Start encryption in a goroutine
	go func() {
		defer pw.Close()

		// Write the encrypted subkey and both nonces to the output stream
		if _, err := pw.Write(subkeyNonce); err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := pw.Write(encSubkey); err != nil {
			pw.CloseWithError(err)
			return
		}

		// Encrypt and write data chunks
		chunk := make([]byte, chunkSize)
		for {
			// Use ReadFull to read exactly chunkSize or less at EOF
			n, err := io.ReadFull(r, chunk)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				pw.CloseWithError(err)
				return
			}

			if n > 0 {
				// Generate nonce and encrypt the chunk
				dataNonce := make([]byte, dataGCM.NonceSize())
				if _, err := rand.Read(dataNonce); err != nil {
					pw.CloseWithError(err)
					return
				}
				if _, err := pw.Write(dataNonce); err != nil {
					pw.CloseWithError(err)
					return
				}

				encryptedChunk := dataGCM.Seal(nil, dataNonce, chunk[:n], nil)
				if _, err := pw.Write(encryptedChunk); err != nil {
					pw.CloseWithError(err)
					return
				}
			}

			// Stop when EOF is reached
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return
			}
		}
	}()

	return pr, nil
}

// DecryptStream decrypts a stream using AES-GCM with a random session-specific subkey
func DecryptStream(key []byte, r io.Reader) (io.Reader, error) {
	// Set up to decrypt the subkey from the input
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Read and decrypt the subkey
	subkeyNonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(r, subkeyNonce); err != nil {
		return nil, err
	}

	encSubkey := make([]byte, gcm.Overhead()+32) // GCM overhead for the 32-byte subkey
	if _, err := io.ReadFull(r, encSubkey); err != nil {
		return nil, err
	}

	subkey, err := gcm.Open(nil, subkeyNonce, encSubkey, nil)
	if err != nil {
		return nil, err
	}

	// Set up AES-GCM for actual data decryption using the subkey
	dataBlock, err := aes.NewCipher(subkey)
	if err != nil {
		return nil, err
	}
	dataGCM, err := cipher.NewGCM(dataBlock)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()

	// Start decryption in a goroutine
	go func() {
		defer pw.Close()

		buffer := make([]byte, chunkSize+dataGCM.Overhead())
		for {
			// Read the data nonce from the input
			dataNonce := make([]byte, dataGCM.NonceSize())
			if _, err := io.ReadFull(r, dataNonce); err != nil {
				pw.CloseWithError(err)
				return
			}

			n, err := r.Read(buffer)
			if err != nil {
				if err != io.EOF {
					pw.CloseWithError(err)
					return
				}
			}

			if n == 0 {
				return
			}

			// Decrypt each chunk and write it to the pipe
			decryptedChunk, err := dataGCM.Open(nil, dataNonce, buffer[:n], nil)
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := pw.Write(decryptedChunk); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
	}()

	return pr, nil
}
