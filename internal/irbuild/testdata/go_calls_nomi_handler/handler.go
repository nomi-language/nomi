// Package nomihandler calls a handler the Nomi program supplies, from many
// goroutines at once, the way a Go server calls a request handler.
package nomihandler

import (
	"fmt"
	"strconv"
	"sync"
)

type Request struct {
	Method  string
	Path    string
	Query   map[string]string
	Headers map[string]string
	Body    []byte
}

type Response struct {
	Status  int64
	Headers map[string]string
	Body    []byte
}

// ServeConcurrently builds one Request per path and calls handler for every
// one of them, each from its own goroutine, all released together. It answers
// one line per request, in path order, so the output does not depend on the
// schedule.
func ServeConcurrently(paths []string, handler func(Request) (Response, error)) ([]string, error) {
	lines := make([]string, len(paths))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := Request{
				Method:  "POST",
				Path:    path,
				Query:   map[string]string{"n": strconv.Itoa(i)},
				Headers: map[string]string{"X-Index": strconv.Itoa(i)},
				Body:    []byte("body " + strconv.Itoa(i)),
			}
			res, err := handler(req)
			if err != nil {
				lines[i] = fmt.Sprintf("%d %s: error %s", i, path, err)
				return
			}
			lines[i] = fmt.Sprintf("%d %s: %d %s %q", i, path, res.Status, res.Headers["content-type"], res.Body)
		}()
	}
	close(start)
	wg.Wait()
	return lines, nil
}
