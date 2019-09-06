package main

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/fatedier/frp/src/models/client"
	"github.com/fatedier/frp/src/utils/log"

	http "net/http"

	"github.com/ginuerzh/gost"
	glog "github.com/go-log/log"

	"crypto/tls"

	"errors"
	"flag"
	"runtime"

	_ "net/http/pprof"
)

var (
	IMEI         string = "123456"
	HeartbeatURL string = "http://192.168.8.105/api/oninfos"
	AUTH         string = ""

	configureFile string
	baseCfg       = &baseConfig{}
)

const (
	KC_RAND_KIND_NUM     = 0 // 纯数字
	KC_RAND_KIND_LOWER   = 1 // 小写字母
	KC_RAND_KIND_UPPER   = 2 // 大写字母
	KC_RAND_KIND_ALL     = 3 // 数字、大小写字母
	AUTH_DEFAULT         = ""
	IMEI_DEFAULT         = "123456"
	HeartbeatURL_DEFAULT = "http://192.168.8.105/api/oninfos"
)

func init() {
	gost.SetLogger(&gost.LogLogger{})

	var (
		printVersion bool
	)

	flag.Var(&baseCfg.route.ChainNodes, "F", "forward address, can make a forward chain")
	flag.Var(&baseCfg.route.ServeNodes, "L", "listen address, can listen on multiple ports")
	flag.StringVar(&configureFile, "C", "", "configure file")
	flag.BoolVar(&baseCfg.Debug, "D", false, "enable debug log")
	flag.BoolVar(&printVersion, "V", false, "print version")
	flag.StringVar(&AUTH, "A", AUTH_DEFAULT, "auth info")
	flag.StringVar(&IMEI, "I", IMEI_DEFAULT, "device serial")
	flag.StringVar(&HeartbeatURL, "U", HeartbeatURL_DEFAULT, "heartbeat url info")
	flag.Parse()

	fmt.Println("====================================")
	fmt.Println(AUTH)
	fmt.Println(IMEI)
	fmt.Println(HeartbeatURL)
	fmt.Println("====================================")

	if printVersion {
		fmt.Fprintf(os.Stderr, "gost %s (%s %s/%s)\n",
			gost.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	if configureFile != "" {
		_, err := parseBaseConfig(configureFile)
		if err != nil {
			glog.Log(err)
			return
		}
	}
	if flag.NFlag() == 0 {
		flag.PrintDefaults()
		return
	}
}

// 随机字符串
func Krand(size int, kind int) []byte {
	ikind, kinds, result := kind, [][]int{[]int{10, 48}, []int{26, 97}, []int{26, 65}}, make([]byte, size)
	is_all := kind > 2 || kind < 0
	rand.Seed(time.Now().UnixNano())
	for i := 0; i < size; i++ {
		if is_all { // random ikind
			ikind = rand.Intn(3)
		}
		scope, base := kinds[ikind][0], kinds[ikind][1]
		result[i] = uint8(base + rand.Intn(scope))
	}
	return result
}

func startProxy() {
	// generate random self-signed certificate.
	if os.Getenv("PROFILING") != "" {
		go func() {
			glog.Log(http.ListenAndServe("127.0.0.1:16060", nil))
		}()
	}

	// NOTE: as of 2.6, you can use custom cert/key files to initialize the default certificate.
	tlsConfig, err := tlsConfig(defaultCertFile, defaultKeyFile)
	if err != nil {
		// generate random self-signed certificate.
		cert, err := gost.GenCertificate()
		if err != nil {
			glog.Log(err)
			os.Exit(1)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}
	gost.DefaultTLSConfig = tlsConfig

	if err := start(); err != nil {
		glog.Log(err)
		os.Exit(1)
	}

	select {}
}

func get(path string) string {
	req, err := http.NewRequest("GET", path, nil)
	if err != nil {
		fmt.Println("new req error")
		return ""
	}
	//	req.Header.Set("Cookie", "name=anny")
	client := &http.Client{}
	resp1, err := client.Do(req)
	if err != nil {
		fmt.Errorf("read body error")
		return ""
	}
	defer resp1.Body.Close()
	bodyb1, err := ioutil.ReadAll(resp1.Body)
	if err != nil {
		fmt.Errorf("read body error")
		return ""
	}
	return string(bodyb1)
}

func base64d(varl string) string {
	decodeBytes, err := base64.StdEncoding.DecodeString(varl)
	if err != nil {
		return varl
	}
	return string(decodeBytes)
}

func main() {
	client.LoadText(base64d(AUTH))

	log.InitLog(client.LogWay, client.LogFile, client.LogLevel, client.LogMaxDays)

	// wait until all control goroutine exit
	var wait sync.WaitGroup
	wait.Add(len(client.ProxyClients))

	for _, client := range client.ProxyClients {
		go func() {
			defer wait.Done()
			for {
				ControlProcess(client, &wait)
				time.Sleep(1 * time.Second)
			}
		}()
	}

	go startProxy()
	go func() {
		timer := time.NewTimer(1 * time.Minute)
		for {
			select {
			case <-timer.C:
				get(fmt.Sprintf("%s/%s", HeartbeatURL, IMEI))
				timer.Reset(1 * time.Minute)

			}
		}
	}()
	wait.Wait()
	log.Warn("All proxy exit!")
}

func start() error {
	gost.Debug = baseCfg.Debug

	var routers []router
	rts, err := baseCfg.route.GenRouters()
	if err != nil {
		return err
	}
	routers = append(routers, rts...)

	for _, route := range baseCfg.Routes {
		rts, err := route.GenRouters()
		if err != nil {
			return err
		}
		routers = append(routers, rts...)
	}

	if len(routers) == 0 {
		return errors.New("invalid config")
	}
	for i := range routers {
		go routers[i].Serve()
	}

	return nil
}
