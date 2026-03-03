import os
from google.oauth2 import service_account
from googleapiclient.discovery import build

def get_drive_service():
    """Uses Service Account for Drive."""
    json_path = '/Users/stephanfeb/.kaggen/credentials/google-credentials.json'
    if not os.path.exists(json_path):
        raise FileNotFoundError(f"Service account JSON not found at {json_path}")
    
    scopes = ['https://www.googleapis.com/auth/drive']
    creds = service_account.Credentials.from_service_account_file(json_path, scopes=scopes)
    return build('drive', 'v3', credentials=creds)

def get_gmail_service():
    """Gmail is currently restricted by Advanced Protection."""
    print("Gmail access is currently blocked by Advanced Protection.")
    return None
