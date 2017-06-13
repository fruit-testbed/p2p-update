class wordpress::conf {
  #Change these values as needed
  
  $root_password = '[PASSWORD]'
  $db_name = '[NAME]'
  $db_user = '[USERNAME]'
  $db_user_password = '[DB_PASSWORD]'
  $db_host = '[IPv4 ADDRESS]'

  #Don't change any of these

  #Evaluates to USER@IP
  $db_user_host = "${db_user}@${db_host}"

  #Evaluates to USER@IP/NAME.*
  $db_user_host_db = "${db_user}@${db_host}/${db_name}.*"
}
