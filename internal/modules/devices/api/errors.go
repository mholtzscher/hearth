package api

import "github.com/danielgtaylor/huma/v2"

func apiError(status int, message string) error {
	return huma.NewError(status, message)
}
