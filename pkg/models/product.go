package models

import "errors"

var ErrProductNotFound = errors.New("product not found")

type Product struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Price    Cents  `json:"price"`
	Category string `json:"category"`
	Image    Image  `json:"image"`
}

type Image struct {
	Thumbnail string `json:"thumbnail"`
	Mobile    string `json:"mobile"`
	Tablet    string `json:"tablet"`
	Desktop   string `json:"desktop"`
}
