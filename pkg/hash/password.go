// Package hash wraps bcrypt for password storage. Nothing in this codebase
// stores or compares plaintext passwords anywhere else.
package hash

import "golang.org/x/crypto/bcrypt"

const cost = bcrypt.DefaultCost

func Password(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ComparePassword returns true only on an exact match. It never returns an
// error to the caller — a malformed hash and a wrong password should be
// indistinguishable to whoever is checking (avoids leaking which case
// occurred through error-message timing/content).
func ComparePassword(hashedPassword, plainPassword string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(plainPassword)) == nil
}
