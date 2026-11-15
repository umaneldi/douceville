from supabase import create_client, Client
from supabase.lib.client_options import ClientOptions
from jose import jwt
from jose.exceptions import JOSEError

from .schemas import DvUser
from .config import config


class TokenError(Exception):
    """Raised when a token is invalid or the user cannot be found."""


def check_token(
    token: str, supabase_jwt_secret: str, supabase_url: str, supabase_admin_key: str
) -> DvUser:
    try:
        payload = jwt.decode(token, supabase_jwt_secret, audience="authenticated")
    except JOSEError as e:
        raise TokenError(str(e))

    user_id = payload["sub"]
    user_email = payload["email"]

    # https://supabase.com/docs/reference/python/admin-api
    supabase = create_client(
        supabase_url,
        supabase_admin_key,
        options=ClientOptions(
            auto_refresh_token=False,
            persist_session=False,
        ),
    )

    response = supabase.table("users").select("*").eq("id", user_id).execute()
    if len(response.data) == 0:
        raise TokenError("No user found in Supabase base")

    user_data = response.data[0]

    supabase.auth.sign_out()

    user = DvUser(
        id=user_id,
        last_name=user_data["last_name"],
        first_name=user_data["first_name"],
        login=user_email,
        permissions=user_data["permissions"],
    )

    return user


def create_access_token(supabase_url: str, supabase_key: str, email: str, password: str) -> str:
    supabase: Client = create_client(supabase_url, supabase_key)
    response = supabase.auth.sign_in_with_password({"email": email, "password": password})

    token = response.session.access_token
    supabase.auth.sign_out()
    return token
