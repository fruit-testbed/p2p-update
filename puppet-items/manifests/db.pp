class wordpress::db {

  class { '::mysql::server':
  
  #Set root password
  root_password => $wordpress::conf::root_password,

  #Create database
  databases => {
    "${wordpress::conf::db_name}" => {
      ensure => 'present',
      charset => 'utf8'
    }
  },

  #Create the user
  users => {
    "${wordpress::conf::db_user_host}" => {
      ensure => present,
      password_hash => mysql_password("${wordpress::conf::db_user_password}")
    }
  },

  #Grant user privileges
  grants => {
    "${wordpress::conf::db_user_host_db}" => {
      ensure => 'present',
      options => ['GRANT'],
      privileges => ['ALL'],
      table => "${wordpress::conf::db_name}.*",
      user => "${wordpress::conf::db_user_host}",
      }
    },
  }

  #Installs MySQL client and all bindings
  class { '::mysql::client':
    require => Class['::mysql::server'],
    bindings_enable => true
  }
}
