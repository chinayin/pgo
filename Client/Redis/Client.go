package Redis

import (
    "bytes"
    "sync"
    "sync/atomic"
    "time"

    "github.com/pinguo/pgo"
)

// Redis Client component, require redis-server 2.6.12+
// configuration:
// redis:
//     class: "@pgo/Client/Redis/Client"
//     prefix: "pgo_"
//     password: ""
//     db: 0
//     maxIdleConn: 10
//     maxIdleTime: "60s"
//     netTimeout: "1s"
//     probInterval: "0s"
//     servers:
//         - "127.0.0.1:6379"
//         - "127.0.0.1:6380"
type Client struct {
    Pool
}

func (c *Client) Get(key string) *pgo.Value {
    newKey := c.BuildKey(key)
    conn := c.GetConnByKey(newKey)
    defer conn.Close(false)

    return pgo.NewValue(conn.Do("GET", newKey))
}

func (c *Client) MGet(keys []string) map[string]*pgo.Value {
    result := make(map[string]*pgo.Value)
    addrKeys, newKeys := c.AddrNewKeys(keys)
    lock, wg := new(sync.Mutex), new(sync.WaitGroup)

    wg.Add(len(addrKeys))
    for addr, keys := range addrKeys {
        go c.RunAddrFunc(addr, keys, wg, func(conn *Conn, keys []string) {
            if items, ok := conn.Do("MGET", keys2Args(keys)...).([]interface{}); ok {
                lock.Lock()
                defer lock.Unlock()
                for i, item := range items {
                    oldKey := newKeys[keys[i]]
                    result[oldKey] = pgo.NewValue(item)
                }
            }
        })
    }

    wg.Wait()
    return result
}

func (c *Client) Set(key string, value interface{}, expire ...time.Duration) bool {
    expire = append(expire, defaultExpire)
    return c.set(key, value, expire[0], "")
}

func (c *Client) MSet(items map[string]interface{}, expire ...time.Duration) bool {
    expire = append(expire, defaultExpire)
    return c.mset(items, expire[0], "")
}

func (c *Client) Add(key string, value interface{}, expire ...time.Duration) bool {
    expire = append(expire, defaultExpire)
    return c.set(key, value, expire[0], "NX")
}

func (c *Client) MAdd(items map[string]interface{}, expire ...time.Duration) bool {
    expire = append(expire, defaultExpire)
    return c.mset(items, expire[0], "NX")
}

func (c *Client) Del(key string) bool {
    newKey := c.BuildKey(key)
    conn := c.GetConnByKey(newKey)
    defer conn.Close(false)

    num, ok := conn.Do("DEL", newKey).(int)
    return ok && num == 1
}

func (c *Client) MDel(keys []string) bool {
    addrKeys, _ := c.AddrNewKeys(keys)
    wg, success := new(sync.WaitGroup), uint32(0)

    wg.Add(len(addrKeys))
    for addr, keys := range addrKeys {
        go c.RunAddrFunc(addr, keys, wg, func(conn *Conn, keys []string) {
            if num, ok := conn.Do("DEL", keys2Args(keys)...).(int); ok && num > 0 {
                atomic.AddUint32(&success, uint32(num))
            }
        })
    }

    wg.Wait()
    return success == uint32(len(keys))
}

func (c *Client) Exists(key string) bool {
    newKey := c.BuildKey(key)
    conn := c.GetConnByKey(newKey)
    defer conn.Close(false)

    num, ok := conn.Do("EXISTS", newKey).(int)
    return ok && num == 1
}

func (c *Client) Incr(key string, delta int) int {
    newKey := c.BuildKey(key)
    conn := c.GetConnByKey(newKey)
    defer conn.Close(false)

    num, _ := conn.Do("INCRBY", newKey, delta).(int)
    return num
}

func (c *Client) set(key string, value interface{}, expire time.Duration, flag string) bool {
    newKey := c.BuildKey(key)
    conn := c.GetConnByKey(newKey)
    defer conn.Close(false)

    var res interface{}
    if len(flag) == 0 {
        res = conn.Do("SET", newKey, value, "EX", expire/time.Second)
    } else {
        res = conn.Do("SET", newKey, value, "EX", expire/time.Second, flag)
    }

    payload, ok := res.([]byte)
    return ok && bytes.Equal(payload, replyOK)
}

func (c *Client) mset(items map[string]interface{}, expire time.Duration, flag string) bool {
    addrKeys, newKeys := c.AddrNewKeys(items)
    wg, success := new(sync.WaitGroup), uint32(0)

    wg.Add(len(addrKeys))
    for addr, keys := range addrKeys {
        go c.RunAddrFunc(addr, keys, wg, func(conn *Conn, keys []string) {
            for _, key := range keys {
                if oldKey := newKeys[key]; len(flag) == 0 {
                    conn.WriteCmd("SET", key, items[oldKey], "EX", expire/time.Second)
                } else {
                    conn.WriteCmd("SET", key, items[oldKey], "EX", expire/time.Second, flag)
                }
            }

            for range keys {
                payload, ok := conn.ReadReply().([]byte)
                if ok && bytes.Equal(payload, replyOK) {
                    atomic.AddUint32(&success, 1)
                }
            }
        })
    }

    wg.Wait()
    return success == uint32(len(items))
}
