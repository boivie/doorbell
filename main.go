package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	dac "github.com/xinsnake/go-http-digest-auth-client"
)

func doorbellRinger(server, password string, ringer <-chan int, client MQTT.Client) {
	for cnt := range ringer {
		if cnt < 0 {
			cnt = 0
		} else if cnt > 10 {
			cnt = 10
		}
		uri := server + "/spc/output/1"

		dr := dac.NewRequest("get_user", password, "GET", uri, "")
		response0, err := dr.Execute()
		success := true
		if err != nil {
			log.Printf("Failed to fetch output status: %v\n", err)
			success = false
		} else {
			b, _ := ioutil.ReadAll(response0.Body)
			log.Printf("Result: %v\n", string(b))
		}

		for i := 0; i < cnt; i++ {
			log.Printf("Chime: %d\n", i)
			dr := dac.NewRequest("put_user", password, "PUT", uri+"/set", "")
			_, err = dr.Execute()
			if err != nil {
				log.Printf("Failed to call spcgw: %v", err)
				success = false
			}

			time.Sleep(500 * time.Millisecond)

			dr.UpdateRequest("put_user", password, "PUT", uri+"/reset", "")
			_, err = dr.Execute()
			if err != nil {
				log.Printf("Failed to call spcgw: %v", err)
				success = false
			}

			time.Sleep(300 * time.Millisecond)
		}

		if success {
			ts := []byte(fmt.Sprintf("%d", time.Now().Unix()))
			client.Publish("doorbell/last_update", 0, true, ts)
		}
	}
}

func publish(client MQTT.Client, topic string, retained bool, payload []byte) {
	fmt.Printf("<- %s = %s\n", topic, string(payload))
	if token := client.Publish(topic, 0, retained, payload); token.Wait() && token.Error() != nil {
		fmt.Printf("PUBLISH ERROR: %v", token.Error())
	}
}

func main() {
	//MQTT.DEBUG = log.New(os.Stdout, "", 0)
	//MQTT.ERROR = log.New(os.Stdout, "", 0)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("signal received, exiting")
		os.Exit(0)
	}()

	hostname, _ := os.Hostname()

	spcServer := flag.String("spcgw-server", "http://localhost:8089", "The full url of the spc GW server")
	spcPassword := flag.String("spcgw-password", "", "A password to authenticate to the SPC GW server")
	mqttServer := flag.String("mqtt-server", "tcp://127.0.0.1:1883", "The full url of the MQTT server to connect to ex: tcp://127.0.0.1:1883")
	mqttClientid := flag.String("mqtt-clientid", hostname+strconv.Itoa(time.Now().Second()), "A clientid for the connection")
	mqttUsername := flag.String("mqtt-username", "", "A username to authenticate to the MQTT server")
	mqttPassword := flag.String("mqtt-password", "", "A password to authenticate to the MQTT server")
	flag.Parse()

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	connOpts := &MQTT.ClientOptions{
		ClientID:             *mqttClientid,
		CleanSession:         true,
		Username:             *mqttUsername,
		Password:             *mqttPassword,
		MaxReconnectInterval: 1 * time.Second,
		KeepAlive:            int64(30 * time.Second),
		TLSConfig:            tls.Config{InsecureSkipVerify: true, ClientAuth: tls.NoClientCert},
	}
	connOpts.AddBroker(*mqttServer)

	var ring = make(chan int)

	connOpts.OnConnect = func(c MQTT.Client) {
		token := c.Subscribe("doorbell/chime", 0, func(client MQTT.Client, message MQTT.Message) {
			if cnt, err := strconv.Atoi(string(message.Payload())); err == nil {
				fmt.Printf("Chime %d times\n", cnt)
				ring <- cnt
			}
		})
		if token.Wait() && token.Error() != nil {
			panic(token.Error())
		}
	}

	connOpts.OnConnectionLost = func(c MQTT.Client, err error) {
		panic(fmt.Sprintf("Disconnected from MQTT server: %v", err))
	}

	client := MQTT.NewClient(connOpts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	} else {
		fmt.Printf("Connected to %s\n", *mqttServer)

		go doorbellRinger(*spcServer, *spcPassword, ring, client)
	}

	for {
		time.Sleep(1 * time.Second)
	}
}
