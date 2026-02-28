module github.com/womat/golib/demo/manchester_sender

go 1.26

require github.com/womat/golib/gpio v0.0.0-20260225083331-bf84a9c74830

require (
	github.com/warthog618/go-gpiocdev v0.9.1 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/womat/golib/gpio => ../../gpio
