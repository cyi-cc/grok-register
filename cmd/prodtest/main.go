package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"grok-register/internal/db"
	"grok-register/internal/mailfetch"
	"grok-register/internal/producer"
)

func main() {
	target := 100
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil {
			target = n
		}
	}

	database, err := db.Init("adskull.db")
	if err != nil {
		panic(err)
	}

	mail := mailfetch.New()
	p := producer.New(database, mail)

	if err := p.Start(target); err != nil {
		panic(err)
	}

	seen := 0
	for {
		s := p.Snapshot()
		for ; seen < len(s.Logs); seen++ {
			fmt.Println("LOG:", s.Logs[seen])
		}
		if !s.Running {
			fmt.Printf("DONE target=%d registered=%d failed=%d pending=%d running=%d msg=%q err=%q\n",
				s.Target, s.Registered, s.Failed, s.Pending, s.RunningNum, s.Message, s.Error)
			break
		}
		time.Sleep(2 * time.Second)
	}
}
