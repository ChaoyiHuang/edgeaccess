package main

import (
        "bytes"
        "crypto/tls"
        "encoding/json"
        "flag"
        "log"
        "math/rand"
        "net/http"
        "os"
        "os/exec"
        "strconv"
        "strings"
        "sync/atomic"
        "time"
        "github.com/gorilla/websocket"
)


type CONFIGURATION struct {
    Crt string `json:"crt"`
    Key string `json:"key"`

    ProjectID string `json:"project_id"`
    EdgeNodeID string `json:"edgenode_id"`
    PlacementURL string `json:"placementURL"`
    RetryPlacementInterval int `json:"retry_placement_interval"`
    RetryEdgeAccessInterval int `json:"retry_edgeaccess_interval"`
}


type MESSAGE struct {
    ID          uint64  `json:"id"`
    TimeStamp   int64   `json:"timestamp"`
    Body        string  `json:"body"` //just CPU utilization here
    Reply       string  `json:"reply"` //one filed to send back by the replier
}

type EDGEACCESS_URL struct {
    Host           string `json:"host"`
    Port           string `json:"port"`
    ToEdged        string `json:"toedged_path"`
    ToEdgeAccess   string `json:"toedgeaccess_path"`
    BiAsync        string `json:"biasync_path"`
}

// global variables used in this file
var globalCounter uint64
var edgeUUID string
var projectUUID string
var edgeAccessURL string
var upLinkCH chan MESSAGE
var downLinkCH chan MESSAGE
var interrupt chan os.Signal
var upLinkConn *websocket.Conn
var downLinkConn *websocket.Conn
var biAsyncLinkConn *websocket.Conn
var conf CONFIGURATION

/* note: error and  exception are not carefully handled here */

func main() {

    if initConfAndVar(&conf) != nil {
        log.Println("Init Configuration failed")
        return
    }

    initLink()

    handleChannel()

    closeChannel()
}

func initConfAndVar(conf *CONFIGURATION) error {

    // global varibles initialization
    edgeUUID      = ""
    edgeAccessURL = ""
    globalCounter =  0

    upLinkCH   =  make(chan MESSAGE)
    downLinkCH =  make(chan MESSAGE)

    upLinkConn   = nil
    downLinkConn = nil
    biAsyncLinkConn  = nil

    //load CLI parameters and configuration
    var f string
    flag.StringVar(&f, "f", "edgd.conf", "path for configuration file")
    // the edgeUUID should be exist in the cert, read from the cli now
    flag.StringVar(&edgeUUID, "uuid", "1", "uuid for the edge node")
    flag.Parse()

    err  := getConfig(conf, f)
    if err != nil {
        log.Println("read configuration failed: ", err)
        return err
    }

    log.Println("conf.edgenode_id", conf.EdgeNodeID)
    log.Println("conf.project_id", conf.ProjectID)
    log.Println("conf.placementURL", conf.PlacementURL)
    log.Println("conf.RetryPlacementInterval", conf.RetryPlacementInterval)
    log.Println("conf.RetryEdgeAccessInterval", conf.RetryEdgeAccessInterval)

    return nil
}

func getConfig( config *CONFIGURATION, f string) error {
    file, _ := os.Open(f)
    defer file.Close()

    decoder := json.NewDecoder(file)
    err := decoder.Decode(config)
    return err
}

func initLink() {
    ea := EDGEACCESS_URL{}
    ea.Host         = ""
    ea.Port         = ""
    ea.ToEdged      = ""
    ea.ToEdgeAccess = ""
    ea.BiAsync      = ""

    for {

        // get EdgeAccess url from placement, the last edgeAccessURL is
        // passed so that the placement can make decision to change EdgeAccess
        // or not
        err := getEdgeAccess(conf.Crt,
                             conf.Key,
                             conf.PlacementURL,
                             &ea)
        if err != nil {
            log.Println("get EdgeAccess URL from placement failed:", err)
            time.Sleep(time.Duration(conf.RetryPlacementInterval) * time.Second)
            continue
        }

        // create uplink/downlink sync connection and async bi-direction connection
        err = createLink(&ea, conf.Crt, conf.Key)
        if err != nil {
            log.Println("create link failed", err)
            //time.Sleep(100 * time.Second)
        } else {
            // all links are established successfully
            log.Println("links to", ea.Host, ea.Port, "completed")
            break
        }
    }
}

func getEdgeAccess( crt, key, placementURL string, ea *EDGEACCESS_URL ) error {

    // edgeUUID and projectID should be stored in the cert.
    // cert, _ := tls.LoadX509KeyPair(crt, key)

    // placementURL should be regulated to "https://host:port"
    dest := placementURL + "/v1.0/edgeaccess"
    bytesBody, err := json.Marshal(ea)
    if err != nil {
        log.Println("Failed in handling getEdgeAccess body")
        return err
    }

    // TODO: use https instead
    client := &http.Client{}
    req, err := http.NewRequest("GET", dest, bytes.NewBuffer(bytesBody))
    if err != nil {
        log.Println("Failed in handling getEdgeAccess NewRequest")
        return err
    }
    req.Header.Add("Accept", "application/json")
    req.Header.Add("project_id", conf.ProjectID)
    req.Header.Add("edgenode_id", conf.EdgeNodeID)
    resp, err := client.Do(req)
    if err != nil {
        log.Println("Failed in handling getEdgeAccess client.Do", err)
        return err
    }
    defer resp.Body.Close()

    log.Println("response from placement code", dest, resp.Status)
    if resp.StatusCode != 200 {
        ea.Host         = ""
        ea.Port         = ""
        ea.ToEdged      = ""
        ea.ToEdgeAccess = ""
        ea.BiAsync      = ""
        return nil
    }

    err = json.NewDecoder(resp.Body).Decode(&ea)
    if err != nil {
        log.Println("Failed in handling NewDecoder response", err)
        return err
    }

    log.Println("Placement Resp, Host", ea.Host)
    log.Println("Placement Resp, Port", ea.Port)
    log.Println("Placement Resp, ToEdged", ea.ToEdged)
    log.Println("Placement Resp, ToEdgeAccess", ea.ToEdgeAccess)
    log.Println("Placement Resp, BiAsync", ea.BiAsync)

    return err
}

func createLink(ea *EDGEACCESS_URL, crt string, key string ) error {

    // cert, err := tls.LoadX509KeyPair(crt, key)
    // if err != nil {
    //     log.Println("loading X509 key pair failed, ", err)
    //     return err
    // }

    // use wss instead
    cert := tls.Certificate{}
    err  := createUpLink("ws://"+ea.Host+":"+ea.Port+ea.ToEdgeAccess, cert)
    if err != nil {
        log.Println("create UpLink failed, ", err)
        return err
    }
    err = createDownLink("ws://"+ea.Host+":"+ea.Port+ea.ToEdged, cert)
    if err != nil {
        log.Println("create DownLink failed, ", err)
        return err
    }
    err = creaseBiAsyncLink("ws://"+ea.Host+":"+ea.Port+ea.BiAsync, cert)
    if err != nil {
        log.Println("create BiAsyncLink failed, ", err)
        return err
    }

    return nil
}


func createUpLink(edgeAccessURL string, cert tls.Certificate) error {

    log.Println("create Up Link", edgeAccessURL)

    dialer := &websocket.Dialer{
    //     TLSClientConfig: &tls.Config{
    //         Certificates:       []tls.Certificate{cert},
    //         InsecureSkipVerify: true,
    //     },
    }
    var err error
    upLinkConn, _, err = dialer.Dial(edgeAccessURL,
                                     http.Header{"edgenode_id": {conf.EdgeNodeID}})
    if err != nil {
        log.Println("dial uplink failed:", edgeAccessURL, err)
        upLinkConn = nil
    }
    return err
}

func createDownLink(edgeAccessURL string, cert tls.Certificate) error {

    log.Println("create Down Link", edgeAccessURL)

    dialer := &websocket.Dialer{
    //     TLSClientConfig: &tls.Config{
    //         Certificates:       []tls.Certificate{cert},
    //         InsecureSkipVerify: true,
    //     },
    }
    var err error
    downLinkConn, _, err = dialer.Dial(edgeAccessURL,
                                       http.Header{"edgenode_id": {conf.EdgeNodeID}})
    if err != nil {
        log.Println("dial downlink failed:", edgeAccessURL, err)
        downLinkConn = nil
    }
    return err
}


func creaseBiAsyncLink(edgeAccessURL string, cert tls.Certificate) error {

    return nil
}


func handleChannel() error {

    go generateMsg()
    go consumerMsg()

    for {
        select {
        case outMsg := <-upLinkCH:
            err := sendReq2EdgeAccess(&outMsg)
            if err != nil {
                renewConn()
            }
        }
    }
}


func sendReq2EdgeAccess( outMsg *MESSAGE ) error {

    req, _ := json.Marshal(&outMsg)
    log.Println("sendReq2EdgeAccess, req:", string(req))

    err := upLinkConn.WriteMessage(websocket.TextMessage, []byte(string(req)))
    if err != nil {
        log.Println("upLink Write:", err)
        return err
    }

    _, resp, err := upLinkConn.ReadMessage()
    if err != nil {
        log.Println("upLink Read:", err)
        return err
    }
    var respMsg MESSAGE
    err = json.Unmarshal([]byte(resp), &respMsg)

    //need to check id in response, and especiall to handle the maximum value of int and it's reverse.
    if respMsg.ID == outMsg.ID {
        log.Println("sendReq2EdgeAccess, replied", respMsg)
    } else {
        log.Println("sendReq2EdgeAccess, wrong order message", respMsg)
    }
    return nil
}


func renewConn() {

    closeChannel()
    initLink()
}

func processDownLinkMsg( inMsg *MESSAGE) error {

    //process downLink request synchrounously
    inMsg.Reply = "touched by EdgeD at" + (time.Now()).Format("2006-01-02 15:04:05")

    return reply2EdgeAccess(inMsg)
}

func reply2EdgeAccess( inMsg *MESSAGE) error {

    reply, _ := json.Marshal(inMsg)
    err := downLinkConn.WriteMessage(websocket.TextMessage, []byte(string(reply)))
    if err != nil {
        log.Println("write back to downLink request:", err)
        return err
    }
    return nil
}


func closeChannel() error {
    if upLinkConn != nil {
        upLinkConn.Close()
        upLinkConn = nil
    }

    if downLinkConn != nil {
        downLinkConn.Close()
        downLinkConn = nil
    }

    if biAsyncLinkConn != nil {
        biAsyncLinkConn.Close()
        biAsyncLinkConn = nil
    }

    return nil
}


func newID() uint64 {
    atomic.AddUint64(&globalCounter, 1)
    return globalCounter
}

func generateMsg( ) {

    for {
        rand.Seed(time.Now().Unix())
        //time.Sleep(time.Duration(rand.Intn(10))*time.Second)
        time.Sleep(30*time.Second)

        var newMsg MESSAGE
        newMsg.ID        = newID()
        newMsg.Body      = strconv.FormatFloat(getCPU(), 'f', 2, 64)
        newMsg.TimeStamp = time.Now().Unix()
        newMsg.Reply     = "" //will be touched by the receiver

        upLinkCH <- newMsg
    }
}


func consumerMsg( ) {
    for {
        _, message, err := downLinkConn.ReadMessage()
        if err != nil {
            log.Println("downLinkConn Read:", err)
            return
        }
        log.Println("downLinkConn recv:", string(message))
        var inMsg MESSAGE
        err = json.Unmarshal([]byte(message), &inMsg)
        if err != nil {
            log.Println("downLinkConn decode err was", err)
        }
        processDownLinkMsg( &inMsg )
    }
}


type Process struct {
    pid int
    cpu float64
}
func getCPU() float64 {

    var totalCPU float64
    totalCPU = 0

    cmd := exec.Command("ps", "aux")
    var out bytes.Buffer
    cmd.Stdout = &out
    err := cmd.Run()
    if err != nil {
        return totalCPU
    }
    processes := make([]*Process, 0)
    for {
        line, err := out.ReadString('\n')
        if err!=nil {
            break;
        }
        tokens := strings.Split(line, " ")
        ft := make([]string, 0)
        for _, t := range(tokens) {
            if t!="" && t!="\t" {
                ft = append(ft, t)
            }
        }
        pid, err := strconv.Atoi(ft[1])
        if err!=nil {
            continue
        }
        cpu, err := strconv.ParseFloat(ft[2], 64)
        if err!=nil {
            return totalCPU
        }
        processes = append(processes, &Process{pid, cpu})
        totalCPU += cpu
    }

    return totalCPU
}
