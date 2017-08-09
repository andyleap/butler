package main

import (
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/andyleap/pocketsphinx"
	"github.com/gordonklaus/portaudio"
)

const (
	hmm  = "/usr/local/share/pocketsphinx/model/en-us/en-us"
	dict = "/usr/local/share/pocketsphinx/model/en-us/cmudict-en-us.dict"

	samplesPerChannel = 512
	sampleRate        = 16000
)

var ()

func main() {
	if err := portaudio.Initialize(); err != nil {
		log.Fatalln("PortAudio init error:", err)
	}
	defer portaudio.Terminate()

	// Init CMUSphinx

	log.Println("Loading CMU PhocketSphinx.")
	log.Println("This may take a while depending on the size of your model.")
	ps := pocketsphinx.NewPocketSphinx(hmm, dict, sampleRate)
	grammar, _ := ioutil.ReadFile("grammar.jsgf")
	ps.ParseJSGF("grammar", string(grammar))
	ps.SetKeyphrase("keyword", "hey butler")
	ps.SetSearch("keyword")

	defer ps.Free()
	l := &Listener{
		dec:          ps,
		audioChannel: make(chan []int16, 50),
	}

	stream, err := portaudio.OpenDefaultStream(1, 0, sampleRate, samplesPerChannel, l.paCallback)
	if err != nil {
		log.Fatalln("PortAudio error:", err)
	}
	defer stream.Close()

	if err := stream.Start(); err != nil {
		log.Fatalln("PortAudio error:", err)
	}
	defer stream.Stop()

	if ps.StartUtt() != nil {
		log.Fatalln("[ERR] Sphinx failed to start utterance")
	}

	go l.Process()

	log.Println("Ready..")
	sig := make(chan os.Signal)
	signal.Notify(sig, os.Kill, os.Interrupt)
	<-sig
}

type Listener struct {
	inSpeech       bool
	uttStarted     bool
	dec            *pocketsphinx.PocketSphinx
	inCommand      bool
	commandTimeout time.Time

	audioChannel chan []int16
	chanDepth    int32
}

// paCallback: for simplicity reasons we process raw audio with sphinx in the this stream callback,
// never do that for any serious applications, use a buffered channel instead.

func (l *Listener) paCallback(in []int16, out []int16, timeInfo portaudio.StreamCallbackTimeInfo, flags portaudio.StreamCallbackFlags) {
	l.audioChannel <- in
	atomic.AddInt32(&l.chanDepth, 1)
}

func (l *Listener) Process() {
	for in := range l.audioChannel {
		curdepth := atomic.AddInt32(&l.chanDepth, -1)
		if curdepth > 10 {
			log.Println("BACKLOG: ", curdepth)
		}

		l.dec.ProcessRaw(in, true, false)
		if l.dec.IsInSpeech() {
			l.inSpeech = true
			if !l.uttStarted {
				l.uttStarted = true
				log.Println("Listening..")
			}
		} else if l.uttStarted {
			// speech -> silence transition, time to start new utterance
			l.dec.EndUtt()
			l.uttStarted = false
			log.Printf("%q", l.dec.GetSearch())
			if l.dec.GetSearch() == "keyword" {
				res, err := l.dec.GetHyp()
				if err == nil {
					log.Println(res)
					l.dec.SetSearch("grammar")
					l.commandTimeout = time.Now().Add(5 * time.Second)
					l.inCommand = true
				}
			} else {
				l.inCommand = false
				l.report() // report results
				l.dec.SetSearch("keyword")
			}
			if l.dec.StartUtt() != nil {
				log.Fatalln("[ERR] Sphinx failed to start utterance")
			}

		} else if l.inCommand && l.commandTimeout.Before(time.Now()) {
			l.dec.EndUtt()
			log.Println("Timeout..")
			l.inCommand = false
			l.dec.SetSearch("keyword")
			if l.dec.StartUtt() != nil {
				log.Fatalln("[ERR] Sphinx failed to start utterance")
			}
		}
	}
}

func (l *Listener) report() {

	hyp, err := l.dec.GetHyp()

	if err != nil {
		log.Println("ah, nothing")
		return
	}
	log.Printf("    > hypothesis: %v", hyp)

}
