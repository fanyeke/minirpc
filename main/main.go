package main

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

	"github.com/fanyeke/minirpc"
)

type Foo int

type Args struct {
	Num1, Num2 int
}

func (foo Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func startServer(addr chan string) {
	var foo Foo
	if err := minirpc.Register(&foo); err != nil {
		log.Fatal("register error: ", err)
	}

	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("network error: ", err)
	}
	log.Println("start rpc server on ", l.Addr())
	addr <- l.Addr().String()
	minirpc.Accept(l)
}

func main() {
	log.SetFlags(0)
	addr := make(chan string)
	go startServer(addr)

	client, _ := minirpc.Dial("tcp", <-addr)
	defer func() { _ = client.Close() }()

	time.Sleep(time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			wg.Add(1)
			args := &Args{Num1: i, Num2: i * i}
			var reply int
			ctx := context.Background()
			if err := client.Call(ctx, "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}
