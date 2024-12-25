# gws客户端

* go版本ws控件

* 客户端
```go
func main() {
	//使用方式
	//先获取client实例
	h := http.Header{}
	h.Add("token", "123")
	c := gws.NewWebsocket(
		gws.WithClientWsUrl(&url.URL{Scheme: "ws", Host: "127.0.0.1:9501", Path: "ws", RawQuery: "id=1"}, h),
	)
	//按照需要设置好监听的闭包函数
	//ws连接成功的时候在这里显示
	c.OnOpen(func(ctx *gws.Context) {
		fmt.Println("ws 连接成功")
		//发送信息 支持 string []byte{}
		_ = ctx.ForthwithSend([]byte(`你好世界`))
	})
	//ws收到消息的时候在这里显示
	c.OnMessage(func(ctx *gws.Context, bytes []byte) {
		fmt.Println("ws 接受到消息: " + string(bytes))
	})
	//ws关闭的时候在这里显示
	c.OnClose(func(ctx *gws.Context) {
		fmt.Println("ws 已断开")
	})
	//连接过程中 读取 写入 等操作出现错误或异常在这里显示
	c.OnError(func(ctx *gws.Context, err error) {
		fmt.Println("ws 出现错误: " + err.Error())
	})

	//执行连接操作
	if err := c.Connect(); err != nil {
		fmt.Println("ws 连接失败", err.Error())
		return
	}
	c.ForthwithSend([]byte("hw"))
	time.Sleep(time.Second)

}

```

* 服务端-Gin

```go


func main() {
	engine := gin.Default()
	engine.GET("/ws", func(ctx *gin.Context) {
	
		c := gws.NewWebsocket(
			gws.WithServerHttp(ctx.Writer, ctx.Request),
			gws.WithServerAuth(func(r *http.Request, ctx *gws.Context) bool {
				//读取请求参数进行校验
				//header  r.Header.Get("id")
				//query   r.URL.Query()["id"][0]
				//body	  data := []byte{} r.Body.Read(data)
				//form    r.FormValue()
				ctx.Storage.Set("id", r.URL.Query()["id"][0])
				return true
			}),
		)
	
		c.OnOpen(func(ctx *gws.Context) {
			fmt.Println("ws 客户端已链接")
		})
	
		//ws收到消息的时候在这里显示
		c.OnMessage(func(ctx *gws.Context, bytes []byte) {
			fmt.Println("ws 接受到消息: ", ctx.Storage.GetString("id"), string(bytes))
			if string(bytes) == "hw" {
				ctx.ForthwithSend([]byte("你好世界"))
			}
		})
		//ws关闭的时候在这里显示
		c.OnClose(func(ctx *gws.Context) {
			fmt.Println("ws 已断开")
		})
		//连接过程中 读取 写入 等操作出现错误或异常在这里显示
		c.OnError(func(ctx *gws.Context, err error) {
			fmt.Println("ws 出现错误: " + err.Error())
		})
		//执行连接操作
		if err := c.Connect(); err != nil {
			fmt.Println("ws 初始化错误")
		}
	})
	engine.Run(":9501")

}

```

* 服务端-golang http

```go

func main() {
	http.HandleFunc("/ws", func(writer http.ResponseWriter, request *http.Request) {
		c := gws.NewWebsocket(
			gws.WithServerHttp(writer, request),
			gws.WithServerAuth(func(r *http.Request, ctx *gws.Context) bool {
				//读取请求参数进行校验
				//header  r.Header.Get("id")
				//query   r.URL.Query()["id"][0]
				//body	  data := []byte{} r.Body.Read(data)
				//form    r.FormValue()
				//ctx.Storage.Set("id", r.URL.Query()["id"][0])
				return true
			}),
		)

		c.OnOpen(func(ctx *gws.Context) {
			fmt.Println("ws 客户端已链接")
		})

		//ws收到消息的时候在这里显示
		c.OnMessage(func(ctx *gws.Context, bytes []byte) {
			fmt.Println("ws 接受到消息: ", ctx.Storage.GetString("id"), string(bytes))
			if string(bytes) == "hw" {
				ctx.ForthwithSend([]byte("你好世界"))
			}
		})
		//ws关闭的时候在这里显示
		c.OnClose(func(ctx *gws.Context) {
			fmt.Println("ws 已断开")
		})
		//连接过程中 读取 写入 等操作出现错误或异常在这里显示
		c.OnError(func(ctx *gws.Context, err error) {
			fmt.Println("ws 出现错误: " + err.Error())
		})
		//执行连接操作
		if err := c.Connect(); err != nil {
			fmt.Println("ws 初始化错误")
		}
	})
	http.ListenAndServe(":9501", nil)
}


```