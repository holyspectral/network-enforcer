module spike/nxadm

go 1.27.3

require spike/common v0.0.0

require (
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/nxadm/tail v1.4.11 // indirect
	golang.org/x/sys v0.0.0-20220908164124-27713097b956 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
)

replace spike/common => ../common
