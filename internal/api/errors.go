// Package api provides S3 API handlers.
package api

import (
	"encoding/xml"
	"net/http"
)

// S3Error represents an S3 error response.
type S3Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId"`

	HTTPStatus int `xml:"-"`
}

func (e *S3Error) Error() string {
	return e.Message
}

// Common S3 errors
var (
	ErrAccessDenied = &S3Error{
		Code:       "AccessDenied",
		Message:    "Access Denied",
		HTTPStatus: http.StatusForbidden,
	}

	ErrBucketAlreadyExists = &S3Error{
		Code:       "BucketAlreadyExists",
		Message:    "The requested bucket name is not available. The bucket namespace is shared by all users of the system. Please select a different name and try again.",
		HTTPStatus: http.StatusConflict,
	}

	ErrBucketAlreadyOwnedByYou = &S3Error{
		Code:       "BucketAlreadyOwnedByYou",
		Message:    "Your previous request to create the named bucket succeeded and you already own it.",
		HTTPStatus: http.StatusConflict,
	}

	ErrBucketNotEmpty = &S3Error{
		Code:       "BucketNotEmpty",
		Message:    "The bucket you tried to delete is not empty.",
		HTTPStatus: http.StatusConflict,
	}

	ErrInvalidBucketName = &S3Error{
		Code:       "InvalidBucketName",
		Message:    "The specified bucket is not valid.",
		HTTPStatus: http.StatusBadRequest,
	}

	ErrNoSuchBucket = &S3Error{
		Code:       "NoSuchBucket",
		Message:    "The specified bucket does not exist.",
		HTTPStatus: http.StatusNotFound,
	}

	ErrNoSuchKey = &S3Error{
		Code:       "NoSuchKey",
		Message:    "The specified key does not exist.",
		HTTPStatus: http.StatusNotFound,
	}

	ErrInvalidAccessKeyId = &S3Error{
		Code:       "InvalidAccessKeyId",
		Message:    "The AWS Access Key Id you provided does not exist in our records.",
		HTTPStatus: http.StatusForbidden,
	}

	ErrSignatureDoesNotMatch = &S3Error{
		Code:       "SignatureDoesNotMatch",
		Message:    "The request signature we calculated does not match the signature you provided.",
		HTTPStatus: http.StatusForbidden,
	}

	ErrRequestTimeTooSkewed = &S3Error{
		Code:       "RequestTimeTooSkewed",
		Message:    "The difference between the request time and the server's time is too large.",
		HTTPStatus: http.StatusForbidden,
	}

	ErrInvalidRequest = &S3Error{
		Code:       "InvalidRequest",
		Message:    "Invalid Request",
		HTTPStatus: http.StatusBadRequest,
	}

	ErrMethodNotAllowed = &S3Error{
		Code:       "MethodNotAllowed",
		Message:    "The specified method is not allowed against this resource.",
		HTTPStatus: http.StatusMethodNotAllowed,
	}

	ErrInternalError = &S3Error{
		Code:       "InternalError",
		Message:    "We encountered an internal error. Please try again.",
		HTTPStatus: http.StatusInternalServerError,
	}

	ErrInvalidRange = &S3Error{
		Code:       "InvalidRange",
		Message:    "The requested range is not satisfiable.",
		HTTPStatus: http.StatusRequestedRangeNotSatisfiable,
	}

	ErrMissingContentLength = &S3Error{
		Code:       "MissingContentLength",
		Message:    "You must provide the Content-Length HTTP header.",
		HTTPStatus: http.StatusLengthRequired,
	}
)

// WriteError writes an S3 error response.
func WriteError(w http.ResponseWriter, err *S3Error) {
	WriteErrorWithResource(w, err, "")
}

// WriteErrorWithResource writes an S3 error response with resource info.
func WriteErrorWithResource(w http.ResponseWriter, err *S3Error, resource string) {
	response := *err
	response.Resource = resource
	response.RequestID = generateRequestID()

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(err.HTTPStatus)

	xml.NewEncoder(w).Encode(response)
}

func generateRequestID() string {
	// Simple request ID generation
	return randomHex(16)
}

func randomHex(n int) string {
	const charset = "0123456789ABCDEF"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[i%len(charset)]
	}
	return string(b)
}
