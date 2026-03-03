import os
from google_auth_oauthlib.flow import InstalledAppFlow
import pickle

SCOPES = [
    'https://www.googleapis.com/auth/gmail.modify',
    'https://www.googleapis.com/auth/drive.file',
    'https://www.googleapis.com/auth/drive.readonly'
]

CLIENT_SECRET_FILE = '/Users/stephanfeb/.kaggen/credentials/google_client_secret.json'
TOKEN_FILE = '/Users/stephanfeb/.kaggen/credentials/google_token.pickle'

def main():
    if not os.path.exists(CLIENT_SECRET_FILE):
        print(f"Error: {CLIENT_SECRET_FILE} not found.")
        return

    flow = InstalledAppFlow.from_client_secrets_file(CLIENT_SECRET_FILE, SCOPES)
    
    print("Please visit the URL that opens in your browser to authorize the application.")
    # Port 0 lets the OS pick an available port, which is more robust
    creds = flow.run_local_server(port=0, prompt='consent', success_message='Kaggen is now authorized! You can close this window.')
    
    with open(TOKEN_FILE, 'wb') as token:
        pickle.dump(creds, token)
    print(f"\nSuccess! Token saved to {TOKEN_FILE}")

if __name__ == '__main__':
    main()
