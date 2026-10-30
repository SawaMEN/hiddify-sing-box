#!/usr/bin/env python3
"""Compare native XDrive with an independent Xray checkout, in separate processes.

Usage: python3 tools/test-xdrive-interop.py --xray-source /path/to/Xray-core
Separate executables keep Xray's version of common APIs out of the native core.
"""
import argparse
import pathlib
import subprocess
import tempfile

NATIVE = r'''
package main
import("context";"fmt";"io";"net";"os";"time"; x "github.com/sagernet/sing-box/transport/v2rayxdrive";"github.com/sagernet/sing-box/option";M "github.com/sagernet/sing/common/metadata";N "github.com/sagernet/sing/common/network")
type echo struct{}
func(echo)NewConnectionEx(ctx context.Context,c net.Conn,_,_ M.Socksaddr,_ N.CloseHandlerFunc){defer c.Close();io.Copy(c,c)}
func main(){ctx:=context.Background();o:=option.V2RayXDriveOptions{Service:"local",RemoteFolder:os.Args[2],FlushIntervalMs:1,PollIntervalMs:2,MaxPollIntervalMs:5};if os.Args[1]=="server"{s,e:=x.NewServer(ctx,nil,o,nil,echo{});must(e);must(s.Start());defer s.Close();fmt.Println("ready");select{}};c,e:=x.NewClient(ctx,nil,M.Socksaddr{},o,nil);must(e);defer c.Close();conn,e:=c.DialContext(ctx);must(e);exchange(conn)}
func must(e error){if e!=nil{panic(e)}}
func exchange(c net.Conn){defer c.Close();must(c.SetDeadline(time.Now().Add(10*time.Second)));payload:="XDrive native interoperability";_,e:=io.WriteString(c,payload);must(e);b:=make([]byte,len(payload));_,e=io.ReadFull(c,b);must(e);if string(b)!=payload{panic("mismatch")};fmt.Println("ok")}
'''
XRAY = r'''
package main
import("context";"fmt";"io";"net";"os";"time";x "github.com/xtls/xray-core/transport/internet/xdrive";"github.com/xtls/xray-core/transport/internet";xn "github.com/xtls/xray-core/common/net";"github.com/xtls/xray-core/transport/internet/stat")
func main(){ctx:=context.Background();o:=&internet.MemoryStreamConfig{ProtocolName:"xdrive",ProtocolSettings:&x.Config{Service:"local",RemoteFolder:os.Args[2],FlushIntervalMs:1,PollIntervalMs:2,MaxPollIntervalMs:5}};if os.Args[1]=="server"{s,e:=x.Serve(ctx,xn.LocalHostIP,0,o,func(c stat.Connection){go func(){defer c.Close();io.Copy(c,c)}()});must(e);defer s.Close();fmt.Println("ready");select{}};c,e:=x.Dial(ctx,xn.Destination{},o);must(e);exchange(c)}
func must(e error){if e!=nil{panic(e)}}
func exchange(c net.Conn){defer c.Close();must(c.SetDeadline(time.Now().Add(10*time.Second)));payload:="XDrive native interoperability";_,e:=io.WriteString(c,payload);must(e);b:=make([]byte,len(payload));_,e=io.ReadFull(c,b);must(e);if string(b)!=payload{panic("mismatch")};fmt.Println("ok")}
'''

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--xray-source', type=pathlib.Path, required=True)
    parser.add_argument('--go', default='go')
    parser.add_argument('--native-go', default='go')
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix='xdrive-interop-') as folder:
        folder = pathlib.Path(folder)
        binaries = {}
        for name, source, cwd, compiler in [('native', NATIVE, root, args.native_go), ('xray', XRAY, args.xray_source, args.go)]:
            src, binary = folder / (name + '.go'), folder / name
            src.write_text(source)
            subprocess.run([compiler, 'build', '-o', str(binary), str(src)], cwd=cwd, check=True)
            binaries[name] = binary
        for server, client in [('native', 'xray'), ('xray', 'native')]:
            storage = folder / (server + '-storage')
            process = subprocess.Popen([str(binaries[server]), 'server', str(storage)], stdout=subprocess.PIPE, text=True)
            try:
                assert process.stdout.readline().strip() == 'ready', 'server did not start'
                subprocess.run([str(binaries[client]), 'client', str(storage)], check=True, timeout=20)
                print(f'PASS: {client} client -> {server} server', flush=True)
            finally:
                process.terminate()
                process.wait(timeout=5)

if __name__ == '__main__':
    main()
