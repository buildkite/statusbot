package statuspage

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) doGet(path string, params fmt.Stringer, out interface{}) error {
	return c.doHTTP("GET", path, params, out)
}

func (c *Client) doPost(path string, params fmt.Stringer, out interface{}) error {
	return c.doHTTP("POST", path, params, out)
}

func (c *Client) doPatch(path string, params fmt.Stringer, out interface{}) error {
	return c.doHTTP("PATCH", path, params, out)
}

func (c *Client) doDelete(path string, params fmt.Stringer, out interface{}) error {
	return c.doHTTP("DELETE", path, params, out)
}

func (c *Client) doHTTP(method, path string, params fmt.Stringer, out interface{}) error {
	var s string
	if params != nil {
		s = params.String()
	}
	r := strings.NewReader(s)
	req, err := http.NewRequest(method, c.url.String()+path, r)
	if err != nil {
		return err
	}
	o := "Oauth " + c.apiKey
	req.Header.Add("Authorization", o)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	return c.doRequest(req, out)
}

func (c *Client) doRequest(req *http.Request, out interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return fmt.Errorf("http error (%s): %s", resp.Status, b)
	}
	if len(b) == 0 {
		b = []byte{'{', '}'}
	}
	err = json.Unmarshal(b, &out)
	if err != nil {
		return err
	}
	return nil
}

func encodeParams(params map[string]interface{}) string {
	s := url.Values{}
	for k, v := range params {
		switch v.(type) {
		case string:
			val := v.(string)
			if val != "" {
				s.Add(k, val)
			}
		case int:
			val := v.(int)
			// TODO: Zero values versus not defined
			if val != 0 {
				s.Add(k, strconv.Itoa(val))
			}
		case []string:
			val := v.([]string)
			for _, v := range val {
				s.Add(k, v)
			}
		case []int:
			val := v.([]int)
			arr := []string{}
			for _, v := range val {
				arr = append(arr, strconv.Itoa(v))
			}
			if len(arr) != 0 {
				s.Add(k, strings.Join(arr, ","))
			}
		case time.Time:
			val := v.(time.Time)
			if !val.IsZero() {
				s.Add(k, val.String())
			}
		case bool:
			if v.(bool) {
				s.Add(k, "true")
			}
		default:
			s.Add(k, fmt.Sprintf("%v", v))
		}
	}
	return s.Encode()
}
