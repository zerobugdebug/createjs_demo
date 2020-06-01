package main

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/gorilla/websocket"
	"github.com/streadway/amqp"
	"github.com/withmandala/go-log"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 2048

	// Time allowed to read the next pong message from the peer.
	pongWait = 10 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Time to wait before force close on connection.
	closeGracePeriod = 10 * time.Second
)

//var addr = flag.String("addr", "localhost:8080", "http service address")

var upgrader = websocket.Upgrader{} // use default options
//var upgrader = websocket.Upgrader{EnableCompression: true} // with Experimental Compression
var loggerInfo = log.New(os.Stdout).WithDebug()

//var connectionRabbitMQ *amqp.Connection
//var channelRabbitMQ *amqp.Channel
//var  chan amqp.Delivery

//var conn = *amqp.Connection

func failOnError(err error, msg string) {
	if err != nil {
		loggerInfo.Fatalf("%s: %s", msg, err)
	}
}

/*
func initRabbitMQ() {
	connectionRabbitMQ, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	failOnError(err, "Failed to connect to RabbitMQ")
	defer connectionRabbitMQ.Close()

	channelRabbitMQ, err := connectionRabbitMQ.Channel()
	failOnError(err, "Failed to open a channel")
	defer channelRabbitMQ.Close()

	messagesRabbitMQ, err := channelRabbitMQ.Consume(
		"WebsocketWorker", // queue
		"",                // consumer
		true,              // auto-ack
		false,             // exclusive
		false,             // no-local
		false,             // no-wait
		nil,               // args
	)
	failOnError(err, "Failed to register a consumer")
	//return conn, ch, nil
}
*/

func functionName() string {
	pc := make([]uintptr, 15)
	n := runtime.Callers(2, pc)
	frames := runtime.CallersFrames(pc[:n])
	frame, _ := frames.Next()
	return frame.Function
}

func ping(ws *websocket.Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		<-ticker.C
		loggerInfo.Debug("Sending ping")
		if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
			loggerInfo.Error("ping:", err)
			break
		}
	}

}

func internalError(ws *websocket.Conn, msg string, err error) {
	loggerInfo.Error(msg, err)
	ws.WriteMessage(websocket.TextMessage, []byte("Internal server error."))
}

func writeWebsocket(ws *websocket.Conn, chanMessage chan []byte) {
	loggerInfo.Debug("Writer started")
	for {
		message := <-chanMessage
		err := ws.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			loggerInfo.Debug("write:", err)
		}
		loggerInfo.Debugf("Sent to websocket: %s", message)
	}
}

func readWebsocket(ws *websocket.Conn, chanMessage chan []byte) {
	defer ws.Close()
	ws.SetReadLimit(maxMessageSize)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			loggerInfo.Error("read: ", err)
			break
		}
		loggerInfo.Debugf("Recv from websocket: %s", message)
		if json.Valid(message) {
			loggerInfo.Debug("Valid JSON, sending to RabbitMQ")
			chanMessage <- message
		} else {
			loggerInfo.Warn("Incorrect JSON!")
		}

	}
}

func publishRabbitMQ(chanRabbitMQ *amqp.Channel, chanMessage chan []byte) {
	loggerInfo.Debug("Publisher started")
	for {
		//message := <-chanMessage
		var message map[string]interface{}
		var messageContentEncoding string = ""
		if err := json.Unmarshal(<-chanMessage, &message); err != nil {
			loggerInfo.Error("Can't parse JSON. Error: ", err)
		}
		//loggerInfo.Debug("Parsed JSON: ", message)
		loggerInfo.Debug("Operation: ", message["operation"])
		loggerInfo.Trace("Data: ", message["data"])
		dataJSON, err := json.Marshal(message["data"])
		if err != nil {
			loggerInfo.Error("Can't convert data to JSON. Error: ", err)
			continue
		}

		loggerInfo.Debug("Full data size: ", len(dataJSON))
		if len(dataJSON) > 100 {
			compressedData := new(bytes.Buffer)
			compressor, _ := flate.NewWriter(compressedData, 5)
			compressor.Write(dataJSON)
			compressor.Close()
			dataJSON = compressedData.Bytes()
			messageContentEncoding = "deflate"
			loggerInfo.Debug("Compressed data size: ", len(dataJSON))
		}

		//loggerInfo.Debug("Data in JSON: ", dataJSON)
		routingKey := message["operation"].(string)
		err = chanRabbitMQ.Publish(
			"main",     // exchange
			routingKey, // routing key
			false,      // mandatory
			false,      // immediate
			amqp.Publishing{
				ContentType:     "text/json",
				ContentEncoding: messageContentEncoding,
				Body:            dataJSON,
			})
		loggerInfo.Debugf("Published to RabbitMQ. Routing key: %s, payload: %s", routingKey, string(dataJSON))
		if err != nil {
			loggerInfo.Error("Failed to publish a message. Error: ", err)
			break
		}
	}
}

/*func monitoringRabbitMQ(chanRabbitMQ *amqp.Channel, chanMessage chan []byte) {

	go func() {
		fmt.Printf("closing: %s", <-c.conn.NotifyClose(make(chan *amqp.Error)))
	}()

	chanRabbitMQ.n
	loggerInfo.Debug("Publisher started")
	message := <-chanMessage
	err := chanRabbitMQ.Publish(
		"main",              // exchange
		"StartBattle.Start", // routing key
		false,               // mandatory
		false,               // immediate
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(message),
		})
	loggerInfo.Debugf("Published to RabbitMQ: %s", message)
	failOnError(err, "Failed to publish a message")
}
*/
func consumeRabbitMQ(chanRabbitMQ *amqp.Channel, chanMessage chan []byte) {
	loggerInfo.Debug("Consumer started")

	messagesRabbitMQ, err := chanRabbitMQ.Consume(
		"WebsocketWorker", // queue
		"",                // consumer
		true,              // auto-ack
		false,             // exclusive
		false,             // no-local
		false,             // no-wait
		nil,               // args
	)
	failOnError(err, "Failed to register a consumer")

	for d := range messagesRabbitMQ {
		loggerInfo.Debugf("Consumed from RabbitMQ: %s", d.Body)
		chanMessage <- d.Body

	}
	reason, ok := <-chanRabbitMQ.NotifyClose(make(chan *amqp.Error))
	loggerInfo.Error("Consumer crashed")
	loggerInfo.Warnf("Consumer closed. Reason: %v, ok: %v", reason, ok)

}

/*
func monitoredRabbitMQChannel(url string) (*amqp.Channel, error) {
	loggerInfo.Debug("Creating monitored RabbitMQ connection...")
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	loggerInfo.Debug("RabbitMQ connection: ", conn)

	loggerInfo.Debug("Creating monitored RabbitMQ channel...")
	ch, err := conn.Channel()

	if err != nil {
		return nil, err
	}

	loggerInfo.Debug("RabbitMQ channel: ", ch)

	channel := ch
	go func() {
		for {
			loggerInfo.Debug("RabbitMQ channel monitoring started")
			reason, ok := <-channel.NotifyClose(make(chan *amqp.Error))
			loggerInfo.Warnf("RabbitMQ channel closed. Reason: %v, ok: %v", reason, ok)
			for {
				loggerInfo.Debug("Reconnecting RabbitMQ connection...")
				time.Sleep(3 * time.Second)
				conn, err := amqp.Dial(url)
				if err == nil {
					loggerInfo.Debug("RabbitMQ connection reconnected")
					for {
						loggerInfo.Debug("Reconnecting RabbitMQ channel...")
						time.Sleep(3 * time.Second)
						ch, err := conn.Channel()
						if err == nil {
							loggerInfo.Debug("RabbitMQ channel reconnected")
							channel = ch
							break
						}
						loggerInfo.Warnf("Can't reconnect RabbitMQ channel. Error: %v", err)
					}
					break
				}
				loggerInfo.Warnf("Can't reconnect RabbitMQ connection. Error: %v", err)

			}
		}
	}()
	return channel, nil
}
*/

func monitoredRabbitMQChannel(conn *amqp.Connection, pipe chan *amqp.Connection, id string) (*amqp.Channel, error) {
	loggerInfo.Debug(id, "- Creating monitored RabbitMQ channel...")
	ch, err := conn.Channel()

	if err != nil {
		return nil, err
	}

	//	loggerInfo.Debug("RabbitMQ channel: ", ch)

	channel := ch
	go func() {
		for {
			loggerInfo.Debug(id, "- RabbitMQ channel monitoring started")
			reason, ok := <-channel.NotifyClose(make(chan *amqp.Error))
			loggerInfo.Warnf("%v - RabbitMQ channel closed. Reason: %v, ok: %v", id, reason, ok)
			//loggerInfo.Error("RabbitMQ connection from pipe: ", conn)
			if ok {
				for {
					loggerInfo.Debug(id, "- Reconnecting RabbitMQ channel...")
					time.Sleep(3 * time.Second)
					conn := <-pipe
					ch, err := conn.Channel()
					if err == nil {
						loggerInfo.Debug(id, "- RabbitMQ channel reconnected")
						channel = ch

						//Non-blocking push new connection back to pipe to chain new connection to other RabbitMQ channels
						select {
						case pipe <- conn:
						default:
						}

						break
					}
					loggerInfo.Warnf("%v - Can't reconnect RabbitMQ channel. Error: %v", id, err)
					//loggerInfo.Debug("RabbitMQ connection: ", conn)
				}

			} else {
				loggerInfo.Debug(id, "- Internal close. Nothing to do.")
				break
			}
		}
	}()
	return channel, nil
}

func monitoredRabbitMQConnection(url string, pipe chan *amqp.Connection, id string) (*amqp.Connection, error) {
	loggerInfo.Debug(id, "- Creating monitored RabbitMQ connection...")
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	//loggerInfo.Debug("RabbitMQ connection: ", conn)

	connection := conn
	go func() {
		for {
			loggerInfo.Debug(id, "- RabbitMQ connection monitoring started")
			reason, ok := <-connection.NotifyClose(make(chan *amqp.Error))
			loggerInfo.Warnf("%v - RabbitMQ connection closed. Reason: %v, ok: %v", id, reason, ok)
			if ok {
				for {
					loggerInfo.Debug(id, "- Reconnecting RabbitMQ connection...")
					time.Sleep(3 * time.Second)
					conn, err := amqp.Dial(url)
					if err == nil {
						loggerInfo.Debug(id, "- RabbitMQ connection reconnected")
						connection = conn
						//loggerInfo.Debug("Reconnected RabbitMQ connection: ", conn)
						pipe <- conn //Send new connection info to the pipe
						break
					}
					loggerInfo.Warnf("%v - Can't reconnect RabbitMQ connection. Error: %v", id, err)
				}
			} else {
				loggerInfo.Debug(id, "- Internal close. Nothing to do.")
				break
			}
		}
	}()
	return connection, nil
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		loggerInfo.Error("upgrade:", err)
		return
	}
	defer ws.Close()

	//wsNetConnection := ws.UnderlyingConn()
	//localAddress := wsNetConnection.LocalAddr()
	//remoteAddress := wsNetConnection.RemoteAddr()
	pipeConnectionRabbitMQ := make(chan *amqp.Connection)

	connectionRabbitMQ, err := monitoredRabbitMQConnection("amqp://guest:guest@localhost:5672/", pipeConnectionRabbitMQ, "[Main]")
	if err != nil {
		loggerInfo.Error("monitoredRabbitMQConnection:", err)
		return
	} //failOnError(err, "Failed to connect to RabbitMQ")
	defer connectionRabbitMQ.Close()

	//channelRabbitMQ, err := connectionRabbitMQ.Channel()
	//failOnError(err, "Failed to open a channel")
	consumeChannelRabbitMQ, err := monitoredRabbitMQChannel(connectionRabbitMQ, pipeConnectionRabbitMQ, "[Consumer]")
	if err != nil {
		loggerInfo.Error("consumeChannelRabbitMQ error:", err)
		return
	}
	defer consumeChannelRabbitMQ.Close()

	publishChannelRabbitMQ, err := monitoredRabbitMQChannel(connectionRabbitMQ, pipeConnectionRabbitMQ, "[Publisher]")
	if err != nil {
		loggerInfo.Error("publishChannelRabbitMQ error:", err)
		return
	}
	defer publishChannelRabbitMQ.Close()

	//loggerInfo.Debug("channelRabbitMQ: ", channelRabbitMQ)
	//loggerInfo.Debug("*channelRabbitMQ: ", *channelRabbitMQ)
	//loggerInfo.Debug("&channelRabbitMQ: ", &channelRabbitMQ)

	//forever := make(chan bool)

	go ping(ws)

	chanMesageFromRabbitMQ := make(chan []byte)
	go consumeRabbitMQ(consumeChannelRabbitMQ, chanMesageFromRabbitMQ)
	go writeWebsocket(ws, chanMesageFromRabbitMQ)

	//chanMesageFromRabbitMQ <- []byte(localAddress.Network())
	//chanMesageFromRabbitMQ <- []byte(localAddress.String())

	//chanMesageFromRabbitMQ <- []byte(remoteAddress.Network())
	//chanMesageFromRabbitMQ <- []byte(remoteAddress.String())

	chanMesageFromWebsocket := make(chan []byte)
	go publishRabbitMQ(publishChannelRabbitMQ, chanMesageFromWebsocket)
	//go pumpStdout(ws, outr, stdoutDone)
	readWebsocket(ws, chanMesageFromWebsocket)

	loggerInfo.Warn("Execution finished!")
	//<-forever
	/*
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				loggerInfo.Debug("read:", err)
				//break
			}
			loggerInfo.Debugf("recv: %s", message)
	*/ //loggerInfo.Debugf("mt: %s", mt)
	/*
		err = channelRabbitMQ.Publish(
			"main",              // exchange
			"StartBattle.Start", // routing key
			false,               // mandatory
			false,               // immediate
			amqp.Publishing{
				ContentType: "text/plain",
				Body:        []byte(message),
			})
		loggerInfo.Debugf(" [x] Sent %s", message)
		failOnError(err, "Failed to publish a message")
	*/
	//		err = c.WriteMessage(mt, d.Body)
	//		if err != nil {
	//			loggerInfo.Debug("write:", err)
	//		}
	//		loggerInfo.Debugf("sent: %s", d.Body)

	//		failOnError(err, "Failed to register a consumer")

	//}
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}

func main() {
	//flag.Parse()
	//log.SetFlags(0)
	//loggerInfo := log.New(os.Stdout).WithDebug()

	http.HandleFunc("/ws", serveWs)
	http.HandleFunc("/", serveHome)
	fs := http.FileServer(http.Dir("./images"))
	http.Handle("/images/", http.StripPrefix("/images/", fs))
	loggerInfo.Fatal(http.ListenAndServe(":8080", nil))
}
