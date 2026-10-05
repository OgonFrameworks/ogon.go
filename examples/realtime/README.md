# Realtime example

A chat room over WS + SSE, with reconnect and presence.

## Run

```bash
ogon dev

# two clients
wscat -c ws://localhost:3000/room/1
wscat -c ws://localhost:3000/room/1

# send a message from one; both see it
```

## Files

```
realtime/
├── ogon.yaml
├── routes/room.go         # live.Handle("/room/{id}", ChatRoom)
├── live/chatroom.go       # the Channel impl
└── room_test.go           # WSClient + reconnect test
```

## Test

```bash
ogon test -run TestLive
```

The test uses `test.WSClient` to open two connections, send from one,
and assert the other receives it (AT-013).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
