package kartapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/pkg/models"
)

// maxBodyBytes caps request bodies. A full order of MaxItems lines is well
// under this.
const maxBodyBytes = 64 << 10

// apiResponse is the spec's ApiResponse schema, used for every error body.
type apiResponse struct {
	Code    int    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

var errorTypes = map[int]string{
	http.StatusBadRequest:          "bad_request",
	http.StatusUnauthorized:        "unauthorized",
	http.StatusForbidden:           "forbidden",
	http.StatusNotFound:            "not_found",
	http.StatusMethodNotAllowed:    "method_not_allowed",
	http.StatusUnprocessableEntity: "validation_error",
	http.StatusTooManyRequests:     "rate_limited",
	http.StatusInternalServerError: "internal_error",
	http.StatusServiceUnavailable:  "unavailable",
}

// unavailableRetryAfter is the Retry-After, in seconds, sent when a data
// store cannot be reached.
const unavailableRetryAfter = "5"

// handleError renders every error as an ApiResponse. Errors that carry a
// status (echo.HTTPError, router 404/405) keep it; an unreachable data store
// is a 503; anything else is a 500 whose details stay in the access log, not
// the response.
func handleError(c *echo.Context, err error) {
	if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
		return
	}

	status := echo.StatusCode(err)
	var message string
	if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
		message = httpErr.Message
	}
	if errors.Is(err, models.ErrUnavailable) {
		status, message = http.StatusServiceUnavailable, "temporarily unavailable, retry later"
		c.Response().Header().Set(echo.HeaderRetryAfter, unavailableRetryAfter)
	}
	if status == 0 || status == http.StatusInternalServerError {
		status, message = http.StatusInternalServerError, "internal server error"
	}
	if message == "" {
		message = strings.ToLower(http.StatusText(status))
	}

	kind, found := errorTypes[status]
	if !found {
		kind = "error"
	}
	if err := c.JSON(status, apiResponse{Code: status, Type: kind, Message: message}); err != nil {
		c.Logger().ErrorContext(c.Request().Context(), "write error response", "err", err)
	}
}

// bindJSON reads a single JSON value from the request body into dst. It
// returns a 400 echo.HTTPError whose message is safe to show to the client.
//
// echo's Bind is not used because it answers 415 and 413 where the spec
// only allows 400, and it also binds path and query parameters.
func bindJSON(c *echo.Context, dst any) error {
	if err := decodeJSON(c.Response(), c.Request(), dst); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get(echo.HeaderContentType)); err != nil || mediaType != echo.MIMEApplicationJSON {
		return errors.New("Content-Type must be application/json")
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	err := decoder.Decode(dst)
	if err == nil {
		if decoder.Decode(&struct{}{}) != io.EOF {
			return errors.New("body must contain a single JSON object")
		}
		return nil
	}

	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	var sizeErr *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF):
		return errors.New("body must not be empty")
	case errors.As(err, &sizeErr):
		return fmt.Errorf("body must not exceed %d bytes", sizeErr.Limit)
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("body is not valid JSON")
	case errors.As(err, &typeErr) && typeErr.Field != "":
		return fmt.Errorf("%s must be of type %s", typeErr.Field, typeErr.Type)
	case errors.As(err, &typeErr):
		return errors.New("body must be a JSON object")
	default:
		return errors.New("body could not be read")
	}
}
