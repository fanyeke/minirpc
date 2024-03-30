package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/fanyeke/minirpc"
)

func startServer(addr chan string) {
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", l.Addr())
	// 将监听的 ":8080" 端口放入管道
	addr <- l.Addr().String()
	minirpc.Accept(l)
}

func main() {
	log.SetFlags(0) // 输出的日志不包括日期时间等信息
	addr := make(chan string)
	go startServer(addr) // 运行服务器

	client, _ := minirpc.Dial("tcp", <-addr) // 开启链接
	defer func() { _ = client.Close() }()    // 关闭链接
	time.Sleep(time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("minirpc req %d", i)
			var reply string
			// 发送一个请求, 这个请求的方法是 `Foo.Sum`, 参数是 `args`, 返回将被写入 `reply`
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.sum errpr:", err)
			}
			log.Println("reply:", reply)
		}(i)
	}
	wg.Wait()
}
