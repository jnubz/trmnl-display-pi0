import requests
api_key = "<your_api_key_here>"
headers = {"access-token": api_key, "User-Agent": "test"}
response = requests.get("https://usetrmnl.com/api/display", headers=headers)
print(response.status_code, response.text)