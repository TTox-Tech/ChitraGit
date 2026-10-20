// Package core holds the domain types shared by every ChitraGit layer.
// It must not import any other internal package.
package core

import (
	"errors"
	"fmt"
)

// Category classifies a failure so that the engine can decide whether to
// retry it and the UI can show a plain-language explanation and a fix.
type Category string

const (
	CatNetwork     Category = "network"
	CatAuth        Category = "auth"
	CatNotFound    Category = "not_found"
	CatRateLimited Category = "rate_limited"
	CatTimeout     Category = "timeout"
	CatUnsafeState Category = "unsafe_state"
	CatConflict    Category = "conflict"
	CatCorrupt     Category = "corrupt"
	CatDisk        Category = "disk"
	CatPermission  Category = "permission"
	CatConfig      Category = "config"
	CatEnvironment Category = "environment"
	CatInternal    Category = "internal"
)

// Retryable reports whether failures of this category are worth retrying.
func (c Category) Retryable() bool {
	switch c {
	case CatNetwork, CatTimeout, CatRateLimited:
		return true
	}
	return false
}

// Error is the structured error type used across ChitraGit.
//
// Message is a short, plain-language explanation meant for end users.
// Detail carries the technical output (for example git's stderr).
// Hint, when set, tells the user how to fix the problem.
type Error struct {
	Category Category
	Message  string
	Detail   string
	Hint     string
	Err      error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Errorf builds an *Error of the given category.
func Errorf(cat Category, format string, args ...any) *Error {
	return &Error{Category: cat, Message: fmt.Sprintf(format, args...)}
}

// CategoryOf returns the category of err, or CatInternal for errors that
// are not *Error.
func CategoryOf(err error) Category {
	var e *Error
	if errors.As(err, &e) {
		return e.Category
	}
	return CatInternal
}
