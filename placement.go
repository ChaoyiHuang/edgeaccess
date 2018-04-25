package main

import (
        "encoding/json"
        "errors"
        "flag"
        "io/ioutil"
        "log"
        "net/http"
        "os"
        "sort"
        "time"
)


type EDGEACCESS_URL struct {
    Host           string `json:"host"`
    Port           string `json:"port"`
    ToEdged        string `json:"toedged_path"`
    ToEdgeAccess   string `json:"toedgeaccess_path"`
    BiAsync        string `json:"biasync_path"`
}


type EDGEACCESS_PING struct {
    ConnNum        int    `json:"conn_num"`
    Host           string `json:"host"`
    Port           string `json:"port"`
    ToEdged        string `json:"toedged_path"`
    ToEdgeAccess   string `json:"toedgeaccess_path"`
    BiAsync        string `json:"biasync_path"`
}

type EdgeAccess struct {
    LastResponse   time.Time `json:"last_response"`
    EdgeAccessHome string    `json:"edgeaccess_home"`
    PingResp       EDGEACCESS_PING `json:"ping_resp"`
}
type EdgeAccesses []EdgeAccess
func (p EdgeAccesses) Len() int { return len(p) }
func (p EdgeAccesses) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p EdgeAccesses) Less(i, j int) bool {
    return p[i].PingResp.ConnNum < p[j].PingResp.ConnNum
}
var listEdgeAccess EdgeAccesses


type CONFIGURATION struct {
    Host string `json:"host"`
    Port string `json:"port"`

    PingInterval int `json:"ping_interval"`
    HeartBroken  int `json:"hearbroken_interval"`

    //a list of EdgeAccess home url
    EdgeAccessHomes []string `json:"edgeaccess_homes"`
}

// global variables used in this file
var conf CONFIGURATION


/* note: error and  exception are not carefully handled here */

func main() {

    if initConfAndVar(&conf) != nil {
        return
    }

    http.HandleFunc("/v1.0/edgeaccess", edgeAccessHandler)

    go healthCollect()

    //use https instead
    http.ListenAndServe(conf.Host+":"+conf.Port, nil)
}

func initConfAndVar(conf *CONFIGURATION) error {

    //load CLI parameters and configuration
    var f string
    flag.StringVar(&f, "f", "placement.conf", "path for configuration file")
    flag.Parse()

    err  := getConfig(conf, f)
    if err != nil {
        log.Println("read configuration failed: ", err)
        return err
    }

    log.Println("configuration conf.Host", conf.Host)
    log.Println("configuration conf.Port", conf.Port)
    log.Println("configuration conf.PingInterval", conf.PingInterval)
    log.Println("configuration conf.HeartBroken", conf.HeartBroken)
    log.Println("configuration conf.EdgeAccessHomes", conf.EdgeAccessHomes)

    // global varibles initialization
    initEdgeAccessList()

    return nil
}

func initEdgeAccessList() {
    var ea EdgeAccess
    ea.LastResponse           = time.Now()
    ea.EdgeAccessHome         = ""
    ea.PingResp.ConnNum       = 0
    ea.PingResp.Host          = ""
    ea.PingResp.Port          = ""
    ea.PingResp.ToEdged       = ""
    ea.PingResp.ToEdgeAccess  = ""
    ea.PingResp.BiAsync       = ""

    for _, v := range conf.EdgeAccessHomes {
        ea.EdgeAccessHome = v
        listEdgeAccess = append(listEdgeAccess, ea)
    }

    log.Println("listEdgeAccess:", listEdgeAccess)
}

func getConfig( config *CONFIGURATION, f string) error {
    file, _ := os.Open(f)
    defer file.Close()

    decoder := json.NewDecoder(file)
    err := decoder.Decode(config)
    return err
}

func edgeAccessHandler(w http.ResponseWriter, r *http.Request) {
    // extract the project-id and edge node id from cert
    // now we just extract edge node id from the header
    edgeUUID := r.Header.Get("edgenode_id")

    // TODO: node validation
    edgeUUID = edgeUUID

    log.Println("receive request from", edgeUUID)

    var lastEU, newEU EDGEACCESS_URL
    if r.Body == nil {
        http.Error(w, "Please send a request body", 400)
        return
    }
    err := json.NewDecoder(r.Body).Decode(&lastEU)
    if err != nil {
        http.Error(w, err.Error(), 400)
        return
    }

    err = getNewEdgeAccess(lastEU.Host, lastEU.Port, &newEU)

    if err != nil {
        http.Error(w, "no EdgeAccess is available, try again later", 404)
        return
    }

    json.NewEncoder(w).Encode(&newEU)
}

func getNewEdgeAccess(lastHost string, lastPort string, ea *EDGEACCESS_URL) error {

    log.Println("Try to find a new proper for", lastHost, lastPort)

    now := time.Now()

    // check to see if the last one is alive yet, it
    // should be selected in priority
    // if lastHost == "", should go to the next "for" loop for faster search
    for _, v := range listEdgeAccess {
        if v.PingResp.Host == lastHost &&
            lastHost != "" &&
            v.PingResp.Port == lastPort &&
            lastPort != "" {
            diff := now.Sub(v.LastResponse)
            if int(diff.Seconds()) < conf.HeartBroken {
                ea.Host         = v.PingResp.Host
                ea.Port         = v.PingResp.Port
                ea.ToEdged      = v.PingResp.ToEdged
                ea.ToEdgeAccess = v.PingResp.ToEdgeAccess
                ea.BiAsync      = v.PingResp.BiAsync
                log.Println("Find the last one, Host", ea.Host,
                            "Port", ea.Port,
                            "ToEdged", ea.ToEdged)
                return nil
            } else {
                // need to find another alive EdgeAccess with least
                // connection number
                break
            }
        }
    }

    // just check out the least/alive one in the sorted list
    // should check whether the connection number reaches the maximum
    // or not
    for _, v := range listEdgeAccess {
        diff := now.Sub(v.LastResponse)
        if int(diff.Seconds()) < conf.HeartBroken {
            ea.Host         = v.PingResp.Host
            ea.Port         = v.PingResp.Port
            ea.ToEdged      = v.PingResp.ToEdged
            ea.ToEdgeAccess = v.PingResp.ToEdgeAccess
            ea.BiAsync      = v.PingResp.BiAsync
            log.Println("Find a new one, Host", ea.Host,
                        "Port", ea.Port,
                        "ToEdged", ea.ToEdged)
            return nil
        }
    }

    log.Println("Error in finding proper edgeaccess")
    return errors.New("Error in finding proper edgeaccess")
}

func healthCollect() {

    //collect heath status of EdgeAccess servers, every minutes
    ticker := time.NewTicker(time.Duration(conf.PingInterval)*time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            for i, _ := range listEdgeAccess {
                //collect heath for each server
                go pingEdgeAccessServer(&listEdgeAccess[i])
            }
        }
    }
}

func pingEdgeAccessServer(ea *EdgeAccess) {
    // TODO: use https instead
    client := &http.Client{}

    log.Println("Ping server", ea.EdgeAccessHome+"/v1.0/ping")
    // use https instead
    // edgeAccessHome should be regulated to shceme https://host:port
    // some server can handle the format https://host:port/v1.0/ping/, but not all
    req, err := http.NewRequest("GET",
                                ea.EdgeAccessHome+"/v1.0/ping", nil)
    if err != nil {
        log.Println(err)
        return
    }

    req.Header.Add("Accept", "application/json")
    resp, err := client.Do(req)
    if err != nil {
        log.Println(err)
        return
    }
    defer resp.Body.Close()

    respBody, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Println(err)
        return
    }

    log.Println("Ping", ea.EdgeAccessHome, "response is", string(respBody))

    result := &EDGEACCESS_PING{}
    err = json.Unmarshal([]byte(respBody), result)
    if err != nil {
        log.Println(err)
        return
    }

    //pointer is passed here, so we just need to update the
    //respose time and connection number.
    ea.LastResponse           = time.Now()
    ea.PingResp.ConnNum       = result.ConnNum
    ea.PingResp.Host          = result.Host
    ea.PingResp.Port          = result.Port
    ea.PingResp.ToEdged       = result.ToEdged
    ea.PingResp.ToEdgeAccess  = result.ToEdgeAccess
    ea.PingResp.BiAsync       = result.BiAsync

    //it may be too freequent sorting if multiple
    //edge access servers reponse in parrarell
    //no lock also means no precise in control
    sort.Sort(listEdgeAccess)
}
