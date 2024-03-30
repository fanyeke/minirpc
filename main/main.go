package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/fanyeke/minirpc"
	"github.com/fanyeke/minirpc/codec"
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
	// 创建一个 string 类型的管道
	addr := make(chan string)
	// 启动服务端
	go startServer(addr)

	// 链接从管道中拿到的地址
	conn, _ := net.Dial("tcp", <-addr)
	defer func() { _ = conn.Close() }()

	time.Sleep(time.Second)

	// 解码默认的编码方式 `Option`
	_ = json.NewEncoder(conn).Encode(minirpc.DefaultOption)
	cc := codec.NewGobCodec(conn)

	for i := 0; i < 5; i++ {
		h := &codec.Header{
			ServiceMethod: "Foo.Sum",
			Seq:           uint64(i),
		}
		// 发送请求
		_ = cc.Write(h, fmt.Sprintf("minirpc req %d", h.Seq))
		// 接受请求
		_ = cc.ReadHeader(h)
		var reply string
		_ = cc.ReadBody(&reply)
		// 打印结果
		log.Println("reply:", reply)
	}
}
