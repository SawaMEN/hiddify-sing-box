package option

import (
 "context"
 "testing"
 "github.com/sagernet/sing/common/json"
 "github.com/sagernet/sing/common/json/badoption"
)

func TestXUIPanelHTTP1RoundTrip(t *testing.T) {
 values:=[]any{HTTPClient{Version:1}, HTTPClientOptions{Version:1}, MASQUEClientEndpointOptions{Version:1}, MASQUEServerEndpointOptions{Version:badoption.Listable[int]{1}}}
 for _,value:=range values {
  data,err:=json.Marshal(value)
  if err!=nil {t.Fatalf("%T marshal: %v",value,err)}
  var decoded any
  switch value.(type) {
  case HTTPClient: decoded=&HTTPClient{}
  case HTTPClientOptions: decoded=&HTTPClientOptions{}
  case MASQUEClientEndpointOptions: decoded=&MASQUEClientEndpointOptions{}
  case MASQUEServerEndpointOptions: decoded=&MASQUEServerEndpointOptions{}
  }
  if err:=json.UnmarshalContext(context.Background(),data,decoded);err!=nil {t.Fatalf("%T round trip: %v (%s)",value,err,data)}
 }
}
