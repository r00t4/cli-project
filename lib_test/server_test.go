package lib_test

import (
	"cliproject/lib"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestSendRequest(t *testing.T) {
	data, err := lib.GetConfig("../config.json")
	if err != nil {
		fmt.Println(err)
	}
	srv := lib.NewServer(&data[0])
	res := make(chan *http.Response)
	go srv.SendRequest("google.com", "GET", res)

	url := ""

	select {
	case p := <-res:
		url = p.Request.URL.Host
	case <-time.After(5 * time.Second):
		fmt.Println("timeout")
	}

	if url != "google.com" {
		t.Errorf("Path = %s; want google.com", url)
	}

}