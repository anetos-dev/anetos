// frontend/ isn't Go: this file keeps the go command (go build ./...,
// go vet ./...) out of node_modules, where some npm packages ship Go
// files.
module spa/frontend

go 1.26
