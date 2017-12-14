# go-statuspage-api
Go library for StatusPage.io API

## Usage

### Base

```golang
package main

import "github.com/yfronto/go-statuspage-api"

const (
  apiKey = "....."
  pageID = "....."
)

func main() {
  c, err := statuspage.NewClient(apiKey, pageID)
  if err != nil {
    // ...
   }
   // Do stuff
}
```

Things are still in progress.
