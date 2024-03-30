package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

// GobCodec
type GobCodec struct {
	conn io.ReadWriteCloser // 建立socket链接的时候得到的实例
	buf  *bufio.Writer      // 为了防止阻塞而创建的缓冲 `Writer`
	dec  *gob.Decoder       // 解码 `Decoder`
	enc  *gob.Encoder       // 编码 `Encoder`
}

var _ Codec = (*GobCodec)(nil) // 不报错的话就保证 `GobCodec` 实现了 `Codec` 的接口

// NewGobCodec 初始化函数
func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}

// ReadHeader 读取 `Header` 信息, 使用 dec 进行对 `Header` 进行解码即可
func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h)
}

// ReadBody 读取 `Body` 信息, 与 ReadHeader() 类似
func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}

// Write 写回相应的函数
func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		_ = c.buf.Flush() // 将缓冲区的数据写回 io.Writer 中
		if err != nil {
			_ = c.Close() // 关闭缓冲区, 释放对应的资源
		}
	}()
	// 对 `Header` 和 `Body` 信息进行编码
	if err := c.enc.Encode(h); err != nil {
		log.Println("rpc codec: gob error encoding header: ", err)
		return err
	}
	if err := c.enc.Encode(body); err != nil {
		log.Panicln("rpc codec: gob error encoding body:", err)
		return nil
	}
	return nil
}
func (c *GobCodec) Close() error {
	return c.conn.Close()
}
