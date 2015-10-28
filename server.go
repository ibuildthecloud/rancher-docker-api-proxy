package dockerapiproxy

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/Sirupsen/logrus"

	rancher "github.com/rancher/go-rancher/client"
	"github.com/rancher/go-rancher/hostaccess"
)

type IoOps interface {
	io.Writer
	Read() ([]byte, error)
}

type Proxy struct {
	client       *rancher.RancherClient
	host, listen string
}

func NewProxy(client *rancher.RancherClient, host, listen string) *Proxy {
	return &Proxy{
		client: client,
		host:   host,
		listen: listen,
	}
}

func (p *Proxy) ListenAndServe() error {
	host, err := p.getHost()
	if err != nil {
		return err
	}

	os.Remove(p.listen)

	l, err := net.Listen("unix", p.listen)
	if err != nil {
		return err
	}

	logrus.Infof("Found host: %v", host.Name)

	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}

		logrus.Info("New connection")
		go p.handle(host, conn)
	}
}

func (p *Proxy) handle(host *rancher.Host, client net.Conn) {
	if err := p.handleError(host, client); err != nil {
		logrus.Errorf("Failed to handle connection: %v", err)
	}
}

func (p *Proxy) handleError(host *rancher.Host, conn net.Conn) error {
	defer conn.Close()

	hostAccessClient := hostaccess.RancherWebsocketClient(*p.client)
	websocket, err := hostAccessClient.GetHostAccess(host.Resource, "dockersocket", nil)
	if err != nil {
		return err
	}

	server := &WebSocketIo{Conn: websocket}
	client := &SocketIo{Conn: conn}

	wg := sync.WaitGroup{}
	wg.Add(2)

	abort := func() {
		wg.Done()
		conn.Close()
		websocket.Close()
	}

	go func() {
		defer abort()
		p.copyLoop(client, server)
	}()

	go func() {
		defer abort()
		p.copyLoop(server, client)
	}()

	wg.Wait()

	logrus.Debugf("Disconnecting")

	return nil
}

func (p *Proxy) copyLoop(from, to IoOps) error {
	con := true

	for con {
		buf, err := from.Read()
		if err != nil {
			return err
		}
		//if err != nil {
		//	if err == io.EOF {
		//		con = false
		//	} else {
		//		return err
		//	}
		//}
		logrus.Debugf("Read %d bytes", len(buf))
		if _, err := to.Write(buf); err != nil {
			return err
		}
		logrus.Debugf("Wrote %d bytes", len(buf))
	}

	return nil
}

func (p *Proxy) getHost() (*rancher.Host, error) {
	host, err := p.client.Host.ById(p.host)
	if err != nil {
		return nil, err
	}

	if host != nil {
		return host, nil
	}

	hosts, err := p.client.Host.List(&rancher.ListOpts{
		Filters: map[string]interface{}{
			"name": p.host,
		},
	})
	if err != nil {
		return nil, err
	}

	if len(hosts.Data) == 0 {
		return nil, fmt.Errorf("Failed to find host", p.host)
	}

	return &hosts.Data[0], nil
}
