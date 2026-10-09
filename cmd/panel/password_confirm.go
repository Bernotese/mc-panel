package main

import "errors"

var errPasswordMismatch = errors.New("passwords do not match")

// askTwice obtains a password via ask (called with the prompt text) and requires a second,
// identical entry.
func askTwice(ask func(prompt string) (string, error)) (string, error) {
	first, err := ask("Password: ")
	if err != nil {
		return "", err
	}
	second, err := ask("Confirm password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errPasswordMismatch
	}
	return first, nil
}
