package xclient

import (
	"context"
	"io"
	"reflect"
	"sync"

	. "github.com/fanyeke/minirpc"
)

type XClient struct {
	d       Discovery
	mode    SelectMode
	opt     *Option
	mu      sync.Mutex
	clients map[string]*Client
}

var _ io.Closer = (*Client)(nil)

func NewXClient(d Discovery, mode SelectMode, opt *Option) *XClient {
	return &XClient{
		d:       d,
		mode:    mode,
		opt:     opt,
		clients: make(map[string]*Client),
	}
}

// Close 关闭当前客户端
func (xc *XClient) Close() error {
	xc.mu.Lock()
	defer xc.mu.Unlock()

	for key, client := range xc.clients {
		_ = client.Close()
		// 记得删除客户端的注册
		delete(xc.clients, key)
	}
	return nil
}
func (xc *XClient) dial(rpcAddr string) (*Client, error) {
	xc.mu.Lock()
	defer xc.mu.Unlock()
	// 通过 `rpcAddr` 获取客户端 `client`
	client, ok := xc.clients[rpcAddr]
	// 客户端存在但是不可用
	if ok && !client.IsAvailable() {
		_ = client.Close()
		delete(xc.clients, rpcAddr)
		client = nil
	}
	// 客户端不存在
	if client == nil {
		var err error
		client, err = XDial(rpcAddr, xc.opt)
		if err != nil {
			return nil, err
		}
		xc.clients[rpcAddr] = client
	}
	// 返回获取到的客户端
	return client, nil
}
func (xc *XClient) call(rpcAddr string, ctx context.Context, serviceMethod string, args, reply interface{}) error {
	// 进行连接
	client, err := xc.dial(rpcAddr)
	if err != nil {
		return err
	}
	return client.Call(ctx, serviceMethod, args, reply)
}
func (xc *XClient) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	// 通过负载均衡选择一个去处, 获得他的 `rpcAddr`
	rpcAddr, err := xc.d.Get(xc.mode)
	if err != nil {
		return err
	}
	// 调用 call
	return xc.call(rpcAddr, ctx, serviceMethod, args, reply)
}

// Broadcast 将请求广播到所有服务实例
// 1. 请求是并发的
// 2. 需要使用互斥锁保证 `error` 和 `reply` 被正确赋值
// 3. 借助 `context.WithCancel` 确保发生错误时快速失败
func (xc *XClient) Broadcast(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	servers, err := xc.d.GetAll()
	if err != nil {
		return err
	}
	// 任务编排
	var wg sync.WaitGroup
	// 互斥锁, 实现正确赋值
	var mu sync.Mutex
	// 拿到子协程中的错误
	var e error
	replyDone := reply == nil
	// 掌控子协程生命周期
	ctx, cancel := context.WithCancel(ctx)
	for _, rpcAddr := range servers {
		wg.Add(1)
		go func(rpcAddr string) {
			defer wg.Done()
			var clonedReply interface{}
			// 如果 `reply` 不为nil, 则创建一个新的 `reply` 类型的实例赋值给 `clonedReply`
			if reply != nil {
				clonedReply = reflect.New(reflect.ValueOf(reply).Elem().Type()).Interface()
			}
			err := xc.call(rpcAddr, ctx, serviceMethod, args, clonedReply)
			mu.Lock()
			if err != nil && e == nil {
				e = err
				cancel()
			}
			if err == nil && !replyDone {
				reflect.ValueOf(reply).Elem().Set(reflect.ValueOf(clonedReply).Elem())
				replyDone = true
			}
			mu.Unlock()
		}(rpcAddr)
	}
	wg.Wait()
	return e
}
