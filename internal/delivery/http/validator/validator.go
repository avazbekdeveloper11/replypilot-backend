// Package validator formats go-playground/validator errors (what Gin's
// ShouldBindJSON returns on a binding-tag failure) into a client-safe
// message.
package validator

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

func FormatError(err error) string {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return err.Error()
	}

	messages := make([]string, 0, len(verrs))
	for _, fe := range verrs {
		messages = append(messages, fmt.Sprintf("%s failed on '%s'", fe.Field(), fe.Tag()))
	}
	return strings.Join(messages, "; ")
}
