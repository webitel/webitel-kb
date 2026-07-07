package main

import (
	"fmt"

	"github.com/webitel/webitel-kb/cmd"
)

//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.42.0 generate --template buf.gen.yaml

func main() {
	if err := cmd.Run(); err != nil {
		fmt.Println(err.Error())
		return
	}
}
