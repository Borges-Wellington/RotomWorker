package internal

type SendItem struct {
    Path    string
    Payload []byte
}

var SendQueue = make(chan SendItem, 256)
