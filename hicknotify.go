package main

import "fmt"
import "net/http"
import "bufio"
import "sync"
import "bytes"
import "regexp"
import "strconv"
import "time"
import "encoding/json"
import "os"

type Event struct {
  etype string
  estate string
  ecount int
  camera *Camera
}

func (e *Event) Complete() bool {
  if len(e.etype) > 0 && len(e.estate) > 0 && e.ecount > 0 {
    return true
  }
  return false
}
func (e *Event) Reset() {
  *e = Event{}
}

func GenerateTimeout(config Config, camera *Camera, lc chan bool, ec chan Event) {
  count := 1
  for {
    select {
      case <-lc:
        break;

      case <-time.After(time.Second * config.WatchdogTime):
        ec <- Event{"watchdog", "lost-signal", count, camera}
        count += 1
        break

    }
  }
}

func GenerateEvents(wg *sync.WaitGroup, config Config, camera Camera, ec chan Event) {
  var last_attempt time.Time

  lc := make(chan bool)
  event := Event{camera: &camera}
  defer wg.Done()

  go GenerateTimeout(config, &camera, lc, ec)

  for {
    if time.Now().Before(last_attempt.Add(time.Second * config.ErrorRetryTime)) {
      fmt.Println("SLEEPING FOR SECONDS", config.ErrorRetryTime)
      time.Sleep(config.ErrorRetryTime * time.Second)
    }
    last_attempt = time.Now()

    client := &http.Client{}
    // This will break the stream of responses, the response has
    // to complete within 10 seconds.
    // client.Timeout = time.Second * 10

    etyper := regexp.MustCompile("eventType>(.*)</eventType")
    estater := regexp.MustCompile("eventState>(.*)</eventState")
    ecountr := regexp.MustCompile("activePostCount>(.*)</activePostCount")
	
    request, err := http.NewRequest("GET", camera.Url, nil)
    if err != nil {
      fmt.Println("REQUEST ERROR", camera, err)
      continue
    }

    request.SetBasicAuth(camera.Username, camera.Password)

    resp, err := client.Do(request)
    if err != nil {
      fmt.Println("DO ERROR", camera, err)
      continue
    }

    reader := bufio.NewReader(resp.Body)
    for {
      line, err := reader.ReadBytes('\n')
      if err != nil {
        fmt.Println("READER ERROR", camera, err)
        break
      }
      // Send keepalive
      lc <- true
      //Uncomment this line to dump all notifications
      fmt.Println(string(line))

      line = bytes.TrimRight(line, "\n\r")
	  
      etypes := etyper.FindSubmatch(line)
      if len(etypes) > 0 {
        event.etype = string(etypes[1])
      }
      estates := estater.FindSubmatch(line)
      if len(estates) > 0 {
        event.estate = string(estates[1])
      }
      ecounts := ecountr.FindSubmatch(line)
      if len(ecounts) > 0 {
        count, _ := strconv.Atoi(string(ecounts[1]))
        event.ecount = count + 1
      }
      if event.Complete() {
        if event.etype != "videoloss" {
        //if event.etype == "videoloss" {
          ec <- event
		//fmt.Println("SENDING NOTIFICATION FOR", event.etype, event.estate, event.ecount)
        }
	        event.Reset()
        event.camera = &camera
      }
    }
  }
}


type Camera struct {
  Url string
  Name string
  Username string
  Password string

}



type Config struct {
  Cameras []Camera
  DomoticzHost string
  DomoticzPort string
  DomoticzBasicAuth bool
  DomoticzUsername string
  DomoticzPassword string
  LineCrossidx string

  DampeningTime time.Duration
  ErrorRetryTime time.Duration
  WatchdogTime time.Duration
}

func LoadConfig() (Config, error) {
  file, err := os.Open("/home/pi/domoticz/scripts/ipcamera/config.json")
  if err != nil {
    return Config{}, err
  }
  decoder := json.NewDecoder(file)
  configuration := Config{}
  err = decoder.Decode(&configuration)
  if err != nil {
    return Config{}, err
  }

  if configuration.DampeningTime <= 0 {
    configuration.DampeningTime = 10
  }
  if configuration.ErrorRetryTime <= 0 {
    configuration.ErrorRetryTime = 5
  }
  if configuration.WatchdogTime <= 0 {
    configuration.WatchdogTime = 5
  }
  return configuration, nil
}

func domoticz(config Config, event Event, now time.Time) {

  if event.etype == "linedetection" && event.estate == "active" {

    fmt.Println("SENDING NOTIFICATION FOR", event, event.camera, now) 
    client := &http.Client{}
    urlstr := fmt.Sprintf("http://%s:%s/json.htm?type=command&param=switchlight&idx=%s&switchcmd=On", config.DomoticzHost, config.DomoticzPort, config.LineCrossidx)
    request, err := http.NewRequest("GET", urlstr, nil)
    if config.DomoticzBasicAuth {
	  request.SetBasicAuth(config.DomoticzUsername, config.DomoticzPassword)
	}
    fmt.Println(urlstr)
    client.Do(request)

    if err != nil {
      fmt.Println(err)
    }
  }



}


func main() {

  config, err := LoadConfig()
  if err != nil {
    fmt.Println("COULD NOT READ CONFIG", err)
    return
  }
  
  ec := make(chan Event)
  wg := &sync.WaitGroup{}
  for _, camera := range config.Cameras {
    fmt.Println("Hiknotify starting... " )
    fmt.Println("Hiknotify listening to Camera : ", config.Cameras )
    fmt.Println("Hiknotify started ..." )
    wg.Add(1)
    go GenerateEvents(wg, config, camera, ec)
  }

  type DampKey struct {
    event string
    camera *Camera
  }

  dampener := make(map[DampKey]time.Time)
  for {
    select {

      case event := <-ec:
        key := DampKey{event.etype, event.camera}
        last, ok := dampener[key]
        now := time.Now()
        if !ok || now.After(last.Add(config.DampeningTime * time.Second)) {
          domoticz(config, event, now)
          dampener[key] = now
        } else {
          fmt.Println("TIME DAMPENED ", event, key)
          dampener[key] = time.Now()
        }
        break;
    }
  }

  wg.Wait()
}
