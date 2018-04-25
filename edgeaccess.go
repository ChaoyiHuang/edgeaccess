package main

import (
        "encoding/json"
        "flag"
        "io"
        "log"
        "net/http"
        "os"
        "sync/atomic"
        "time"
        "github.com/gorilla/websocket"
)


type CONFIGURATION struct {
    Host string `json:"host"`
    Port string `json:"port"`

    Crt string `json:"crt"`
    Key string `json:"key"`

    ToEdged string `json:"toedged_path"`
    ToEdgeAccess string `json:"toedgeaccess_path"`
    BiAsync string `json:"biasync_path"`
}


type MESSAGE struct {
    ID          uint64  `json:"id"`
    TimeStamp   int64   `json:"timestamp"`
    Body        string  `json:"body"` //just CPU utilization here
    Reply       string  `json:"reply"` //one filed to send back by the replier
}


type CONN_SESSION struct{
    upLinkCH chan MESSAGE
    downLinkCH chan MESSAGE
    upLinkConn *websocket.Conn
    downLinkConn *websocket.Conn
    biAsyncLinkConn *websocket.Conn
}

// global variables used in this file
var globalCounter uint64
var interrupt chan os.Signal
var conf CONFIGURATION
var mapSession map[string]*CONN_SESSION

/* note: error and  exception are not carefully handled here */

func main() {

    err := initConfAndVar(&conf)
    if err != nil {
        log.Println("read configuration failed: ", err)
        return
    }

    StartServer()
}

func initConfAndVar(conf *CONFIGURATION) error {

    // global varibles initialization
    globalCounter =  0

    mapSession = make(map[string]*CONN_SESSION)

    //load CLI parameters and configuration
    var f string
    flag.StringVar(&f, "f", "edgaccess.conf", "path for configuration file")
    flag.Parse()

    return getConfig(conf, f)
}

func newID() uint64 {
    atomic.AddUint64(&globalCounter, 1)
    return globalCounter
}


func getConfig( config *CONFIGURATION, f string) error {
    file, _ := os.Open(f)
    defer file.Close()

    decoder := json.NewDecoder(file)
    err := decoder.Decode(config)

    if err == nil {
        log.Println("configuration: Host", config.Host)
        log.Println("configuration: Port", config.Port)
        log.Println("configuration: ToEdged", config.ToEdged)
        log.Println("configuration: ToEdgeAccess", config.ToEdgeAccess)
        log.Println("configuration: BiAsync", config.BiAsync)
    }

    return err
}

func StartServer() {

    log.Println("Start Server...")

    http.HandleFunc("/v1.0/ping", handlePing)
    http.HandleFunc(conf.ToEdged, handleSync2Edged)
    http.HandleFunc(conf.ToEdgeAccess, handleSync2EdgeAccess)
    http.HandleFunc("/v1.0/ping2edged", handlePing2Edged)

    //use https instead
    //http.ListenAndServeTLS(conf.Host+":"+conf.Port, conf.Crt, conf.Key, nil)
    http.ListenAndServe(conf.Host+":"+conf.Port, nil)
}

func handleSync2Edged(w http.ResponseWriter, r *http.Request) {

    edgenode_id := r.Header.Get("edgenode_id")
    log.Println("handleSync2Edged ...", edgenode_id)

    upgrader := websocket.Upgrader{}
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    if mapSession[edgenode_id] == nil {
        newSession := new(CONN_SESSION)
        mapSession[edgenode_id] = newSession
    }

    //add the session to the map
    mapSession[edgenode_id].downLinkConn = conn
    mapSession[edgenode_id].downLinkCH   = make(chan MESSAGE)

    log.Println("Downlink for", edgenode_id, "established...")

    // go handleDownLink(edgenode_id)
}

func handleDownLink(edgenode_id string) {

    edge := mapSession[edgenode_id]
    if edge == nil { return }

    defer func() {
        if r := recover(); r != nil {
            log.Println("Panic captured, removeConn in handleDownLink")
            removeConn(edgenode_id)
            return
        }
    }()

    for {
        select {
        case downMsg := <-edge.downLinkCH:
            req, _ := json.Marshal(downMsg)
            err    := edge.downLinkConn.WriteMessage(websocket.TextMessage,
                                                     []byte(string(req)))
            if err != nil {
                log.Println("failed to write message to: edgenode_id",
                            edgenode_id, "for req", string(req))
                removeConn(edgenode_id)
                return
            }

            log.Println("Send msg to downlink: edgenode_id", edgenode_id,
                        "req", string(req))

            //should handle timeout and check the return id here
            //discard lower order id
            _, reply, err := edge.downLinkConn.ReadMessage()
            if err != nil {
                log.Println("read reply failed:", err)
                removeConn(edgenode_id)
                return
            }

            log.Println("recv:", reply, "from", edgenode_id)
        }
    }
}

func removeConn(edgenode_id string) {
    edge := mapSession[edgenode_id]
    if edge == nil { return }

    edge.downLinkConn.Close()
    edge.upLinkConn.Close()
    close(edge.downLinkCH)
    close(edge.upLinkCH)
    delete(mapSession, edgenode_id)
}

func handleSync2EdgeAccess(w http.ResponseWriter, r *http.Request) {

    edgenode_id := r.Header.Get("edgenode_id")
    log.Println("handleSync2EdgeAccess...", edgenode_id)

    upgrader := websocket.Upgrader{}
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }


    //add the session to the map
    if mapSession[edgenode_id] == nil {
        newSession := new(CONN_SESSION)
        mapSession[edgenode_id] = newSession
    }

    //add the session to the map
    mapSession[edgenode_id].upLinkConn = conn
    mapSession[edgenode_id].upLinkCH   = make(chan MESSAGE)

    log.Println("Uplink for", edgenode_id, "established...")

    go handleUpLink(edgenode_id)
}

func handleUpLink(edgenode_id string) {

    edge, is_present := mapSession[edgenode_id]
    if !is_present { return }

    defer func() {
        if r := recover(); r != nil {
            log.Println("Panic captured, removeConn in handleUpLink")
            removeConn(edgenode_id)
            return
        }
    }()

    for {
        var inMsg MESSAGE
        msgType, msg, err := edge.upLinkConn.ReadMessage()
        if err != nil {
            log.Println("edge.upLinkConn.ReadMessage failed", err)
            removeConn(edgenode_id)
            return
        }
        err = json.Unmarshal([]byte(msg), &inMsg)
        inMsg.Reply = "touched by EdgeAccess at" + (time.Now()).Format("2006-01-02 15:04:05")
        reply, _ := json.Marshal(&inMsg)
        err = edge.upLinkConn.WriteMessage(msgType, []byte(string(reply)))
        if err != nil {
            log.Println("edge.upLinkConn.WriteMessage failed", err)
            removeConn(edgenode_id)
            return
        }
        log.Println("handleUpLink:", err, string(reply))
    }
}


func handlePing2Edged(w http.ResponseWriter, r *http.Request) {

    edgenode_id := r.URL.Query().Get("edgenode_id")
    msg         := r.URL.Query().Get("msg")

    if edgenode_id == "" || msg == "" {
        log.Println("invalid GET params:", edgenode_id, msg);
    }
    log.Println("handlePing2Edged, GET params were:", edgenode_id, msg)

    if mapSession[edgenode_id] == nil {
        log.Println("this node not servered by me", edgenode_id);
        return
    }
    edge := mapSession[edgenode_id]

    defer func() {
        if r := recover(); r != nil {
            log.Println("Panic captured, removeConn in handlePing2Edged")
            removeConn(edgenode_id)
            return
        }
    }()

    var newMsg MESSAGE
    newMsg.ID        = newID()
    newMsg.Body      = msg
    newMsg.TimeStamp = time.Now().Unix()
    newMsg.Reply     = "" //will be touched by the receiver

    req, _ := json.Marshal(&newMsg)
    err    := edge.downLinkConn.WriteMessage(websocket.TextMessage,
                                             []byte(string(req)))
    if err != nil {
        log.Println("failed to write message to: edgenode_id",
                    edgenode_id, "for req", string(req))
        removeConn(edgenode_id)
        return
    }

    log.Println("Ping msg to edgenode_id", edgenode_id,
                "req", string(req))

    //should handle timeout and check the return id here
    //discard lower order id
    _, reply, err := edge.downLinkConn.ReadMessage()
    if err != nil {
        log.Println("read ping reply failed:", err)
        removeConn(edgenode_id)
        return
    }

    io.WriteString(w, "Reply from " + edgenode_id + " is "+ string(reply))
    log.Println("Ping resp", edgenode_id, string(reply))
}

type EDGEACCESS_PING struct {
    ConnNum        int    `json:"conn_num"`
    Host           string `json:"host"`
    Port           string `json:"port"`
    ToEdged        string `json:"toedged_path"`
    ToEdgeAccess   string `json:"toedgeaccess_path"`
    BiAsync        string `json:"biasync_path"`
}


func handlePing(w http.ResponseWriter, r *http.Request) {

    log.Println("hanldePing...")

    pingRsp := EDGEACCESS_PING{}
    pingRsp.ConnNum       = len(mapSession)
    pingRsp.Host          = conf.Host
    pingRsp.Port          = conf.Port
    pingRsp.ToEdged       = conf.ToEdged
    pingRsp.ToEdgeAccess  = conf.ToEdgeAccess
    pingRsp.BiAsync       = conf.BiAsync

    jsonBody, err := json.Marshal(&pingRsp)
    if err != nil{
        log.Println("handlePing json.Marshal failed")
    }

    w.Header().Set("Content-Type","application/json")
    w.WriteHeader(http.StatusOK)
    w.Write(jsonBody)
}
