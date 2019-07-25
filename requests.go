package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

func send(request *http.Request) (io.ReadCloser, error) {
	// https://medium.com/@nate510/don-t-use-go-s-default-http-client-4804cb19f779
	client := &http.Client{
		Timeout: time.Second * 10,
	}

	var r io.ReadCloser
	if request.Method == http.MethodGet {
		resp, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		r = resp.Body
		if resp.StatusCode != 200 {
			defer resp.Body.Close()
			rBytes, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("Response is not 200 (%v), error: %s", resp.StatusCode, rBytes)
		}
	} else {
		return nil, fmt.Errorf("no method on Request struct")
	}
	return r, nil
}
