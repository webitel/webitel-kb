package main

import (
	"fmt"

	"github.com/webitel/webitel-kb/cmd"
)

func main() {
	if err := cmd.Run(); err != nil {
		fmt.Println(err.Error())
		return
	}
}
