import { useState, useEffect, useCallback } from 'react'
import { useAuth } from '../contexts/AuthContext'
import { useToast } from './Toast'
import {
  Users,
  UserCheck,
  UserX,
  Clock,
  Search,
  Loader2,
  ChevronLeft,
  ChevronRight,
  RefreshCw,
  Eye,
  ShieldCheck,
  Github,
  MessageCircle,
  Send,
  Key,
  Shield,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Badge } from './ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from './ui/table'
import { Select } from './ui/select'
import { StatCard } from './StatCard'
import { cn } from '../lib/utils'
import { UserAnalysisDialog } from './UserAnalysisDialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs'
import { AffiliateStats } from './AffiliateStats'
import { PanelWhitelist } from './PanelWhitelist'



interface ActivityStats {
  total_users: number
  active_users: number
  inactive_users: number
  very_inactive_users: number
  never_requested: number
}

// 分组信息
interface GroupInfo {
  group_name: string
  user_count: number
}

// 注册来源标签
const SOURCE_LABELS: Record<string, { label: string; icon: typeof Github }> = {
  github: { label: 'GitHub', icon: Github },
  wechat: { label: '微信', icon: MessageCircle },
  telegram: { label: 'Telegram', icon: Send },
  discord: { label: 'Discord', icon: MessageCircle },
  oidc: { label: 'OIDC', icon: Shield },
  linux_do: { label: 'LinuxDO', icon: Users },
  password: { label: '密码注册', icon: Key },
}

interface UserInfo {
  id: number
  username: string
  display_name: string | null
  email: string | null
  role: number
  status: number
  quota: number
  used_quota: number
  request_count: number
  group: string | null
  last_request_time: number | null
  activity_level: string
  linux_do_id: string | null
  source?: string
}

export function UserManagement() {
  const { token } = useAuth()
  const { showToast } = useToast()

  const [activeTab, setActiveTab] = useState<'list' | 'affiliate' | 'panel-whitelist'>('list')
  const [stats, setStats] = useState<ActivityStats | null>(null)
  const [users, setUsers] = useState<UserInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)
  const [pageSize] = useState(20)
  const [total, setTotal] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [search, setSearch] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [activityFilter, setActivityFilter] = useState<string>('all')
  const [refreshing, setRefreshing] = useState(false)

  // 用户分析弹窗状态
  const [analysisDialogOpen, setAnalysisDialogOpen] = useState(false)
  const [selectedUser, setSelectedUser] = useState<{ id: number; username: string } | null>(null)

  // 邀请用户列表状态
  const [invitedUsers, setInvitedUsers] = useState<{
    inviter: { user_id: number; username: string; display_name: string; aff_code: string; aff_count: number; aff_quota: number; aff_history: number } | null
    items: Array<{ user_id: number; username: string; display_name: string; email: string; status: number; quota: number; used_quota: number; request_count: number; group: string; role: number }>
    total: number
    stats: { total_invited: number; active_count: number; banned_count: number; total_used_quota: number; total_requests: number }
  } | null>(null)
  const [invitedLoading, setInvitedLoading] = useState(false)
  const [invitedPage, setInvitedPage] = useState(1)

  // 分组 / 来源筛选
  const [groups, setGroups] = useState<GroupInfo[]>([])
  const [groupFilter, setGroupFilter] = useState('')
  const [sourceFilter, setSourceFilter] = useState('')

  // Linux.do 用户名查询状态
  const [linuxDoLookupLoading, setLinuxDoLookupLoading] = useState<string | null>(null)

  const apiUrl = import.meta.env.VITE_API_URL || ''

  const getAuthHeaders = useCallback(() => ({
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
  }), [token])

  const fetchStats = useCallback(async (quick = false) => {
    try {
      const params = quick ? '?quick=true' : ''
      const response = await fetch(`${apiUrl}/api/users/stats${params}`, { headers: getAuthHeaders() })
      const data = await response.json()
      if (data.success) {
        setStats(data.data)
        // 如果是快速模式且活跃度数据为0，异步加载完整数据
        if (quick && data.data.active_users === 0 && data.data.inactive_users === 0 && data.data.very_inactive_users === 0) {
          // 延迟加载完整统计，不阻塞用户列表
          setTimeout(() => fetchStats(false), 100)
        }
      }
    } catch (error) {
      console.error('Failed to fetch stats:', error)
    }
  }, [apiUrl, getAuthHeaders])

  // 获取可用分组列表
  const fetchGroups = useCallback(async () => {
    try {
      const response = await fetch(`${apiUrl}/api/users/groups`, { headers: getAuthHeaders() })
      const data = await response.json()
      if (data.success) {
        setGroups(data.data.items)
      }
    } catch (error) {
      console.error('Failed to fetch groups:', error)
    }
  }, [apiUrl, getAuthHeaders])

  const fetchUsers = useCallback(async () => {
    setLoading(true)
    try {
      const params = new URLSearchParams({
        page: page.toString(),
        page_size: pageSize.toString(),
      })
      if (search) params.append('search', search)
      if (activityFilter && activityFilter !== 'all') params.append('activity', activityFilter)
      if (groupFilter) params.append('group', groupFilter)
      if (sourceFilter) params.append('source', sourceFilter)

      const response = await fetch(`${apiUrl}/api/users?${params}`, { headers: getAuthHeaders() })
      const data = await response.json()
      if (data.success) {
        setUsers(data.data.items)
        setTotal(data.data.total)
        setTotalPages(data.data.total_pages)
      }
    } catch (error) {
      console.error('Failed to fetch users:', error)
      showToast('error', '加载用户列表失败')
    } finally {
      setLoading(false)
    }
  }, [apiUrl, getAuthHeaders, page, pageSize, search, activityFilter, groupFilter, sourceFilter, showToast])

  const handleSearch = () => {
    setPage(1)
    setSearch(searchInput)
  }

  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') handleSearch()
  }

  useEffect(() => {
    fetchStats(true)  // 首次加载使用快速模式
    fetchGroups()  // 获取分组列表
  }, [fetchStats, fetchGroups])

  useEffect(() => {
    fetchUsers()
  }, [fetchUsers])

  const handleRefresh = async () => {
    setRefreshing(true)
    await Promise.all([fetchUsers(), fetchStats()])
    setRefreshing(false)
    showToast('success', '数据已刷新')
  }

  // 打开用户分析弹窗
  const openUserAnalysis = (userId: number, username: string) => {
    setSelectedUser({ id: userId, username })
    setAnalysisDialogOpen(true)
    setInvitedUsers(null)
    setInvitedPage(1)
  }

  // 获取邀请用户列表
  const fetchInvitedUsers = useCallback(async () => {
    if (!selectedUser || !analysisDialogOpen) return
    setInvitedLoading(true)
    try {
      const response = await fetch(`${apiUrl}/api/users/${selectedUser.id}/invited?page=${invitedPage}&page_size=10`, { headers: getAuthHeaders() })
      const res = await response.json()
      if (res.success) {
        setInvitedUsers(res.data)
      }
    } catch (e) {
      console.error('Failed to fetch invited users:', e)
    } finally {
      setInvitedLoading(false)
    }
  }, [apiUrl, getAuthHeaders, selectedUser, analysisDialogOpen, invitedPage])

  useEffect(() => {
    if (analysisDialogOpen && selectedUser) {
      fetchInvitedUsers()
    }
  }, [analysisDialogOpen, selectedUser, invitedPage, fetchInvitedUsers])

  const formatQuota = (quota: number) => `$${(quota / 500000).toFixed(2)}`

  // 格式化最后请求时间
  // 快速模式下 last_request_time 为 null，根据 request_count 判断
  const formatLastRequest = (user: UserInfo) => {
    if (user.last_request_time) {
      return new Date(user.last_request_time * 1000).toLocaleString('zh-CN', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      })
    }
    // 快速模式：无精确时间
    if (user.request_count > 0) {
      return <span className="text-muted-foreground">有请求记录</span>
    }
    return <span className="text-muted-foreground">从未</span>
  }

  const getActivityBadge = (level: string) => {
    const baseClass = "w-[92px] justify-center"
    switch (level) {
      case 'active':
        return <Badge variant="success" className={baseClass}>活跃</Badge>
      case 'inactive':
        return <Badge variant="warning" className={baseClass}>不活跃</Badge>
      case 'very_inactive':
        return <Badge variant="destructive" className={baseClass}>非常不活跃</Badge>
      case 'never':
        return <Badge variant="secondary" className={baseClass}>从未请求</Badge>
      default:
        return <Badge variant="outline" className={baseClass}>{level}</Badge>
    }
  }

  const getRoleBadge = (role: number) => {
    const baseClass = "w-[92px] justify-center whitespace-nowrap"
    switch (role) {
      case 1:
        return <Badge variant="outline" className={cn(baseClass, "text-muted-foreground font-normal border-muted-foreground/20")}>普通用户</Badge>
      case 10:
        return <Badge className={cn(baseClass, "bg-blue-500 hover:bg-blue-600 border-none")}>管理员</Badge>
      case 100:
        return (
          <Badge className={cn(baseClass, "bg-gradient-to-r from-amber-500 to-orange-600 hover:from-amber-600 hover:to-orange-700 text-white border-none shadow-sm")}>
            <ShieldCheck className="w-3 h-3 mr-1 shrink-0" />
            超级管理员
          </Badge>
        )
      default:
        return <Badge variant="secondary" className={baseClass}>角色{role}</Badge>
    }
  }

  const getStatusBadge = (status: number) => {
    const baseClass = "w-[64px] justify-center"
    switch (status) {
      case 1:
        return <Badge variant="success" className={baseClass}>正常</Badge>
      case 2:
        return <Badge variant="destructive" className={baseClass}>禁用</Badge>
      default:
        return <Badge variant="outline" className={baseClass}>未知</Badge>
    }
  }

  return (
    <div className="space-y-6 animate-in fade-in duration-500">
      {/* Header */}
      <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
        <div>
          <h2 className="text-3xl font-bold tracking-tight">用户管理</h2>
          <p className="text-muted-foreground mt-1">查看和管理所有用户及其状态</p>
        </div>
        <Button variant="outline" size="sm" onClick={handleRefresh} disabled={refreshing || loading} className="h-9">
          <RefreshCw className={cn("h-4 w-4 mr-2", refreshing && "animate-spin")} />
          刷新
        </Button>
      </div>

      <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as 'list' | 'affiliate' | 'panel-whitelist')} className="w-full">
        <TabsList className="grid w-full max-w-xl grid-cols-3">
          <TabsTrigger value="list" className="gap-2">
            <Users className="h-4 w-4" />
            用户列表
          </TabsTrigger>
          <TabsTrigger value="affiliate" className="gap-2">
            <ShieldCheck className="h-4 w-4" />
            邀请返利统计
          </TabsTrigger>
          <TabsTrigger value="panel-whitelist" className="gap-2">
            <Shield className="h-4 w-4" />
            面板白名单
          </TabsTrigger>
        </TabsList>

        {/* forceMount + data-state hide：保留列表 tab 的状态/筛选/分页，
            切到邀请返利统计再切回不会触发重新拉数据。 */}
        <TabsContent value="list" forceMount className="data-[state=inactive]:hidden mt-6 space-y-6">

      {/* Activity Stats Cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard
          title="活跃用户"
          value={stats?.active_users || 0}
          subValue={stats?.active_users === 0 && stats?.inactive_users === 0 && stats?.very_inactive_users === 0 && (stats?.never_requested || 0) > 0 ? "计算中..." : "7天内有请求"}
          icon={UserCheck}
          color="green"
          onClick={() => { setActivityFilter('active'); setPage(1) }}
          className={cn(activityFilter === 'active' && "ring-2 ring-primary ring-offset-2")}
        />
        <StatCard
          title="不活跃用户"
          value={stats?.inactive_users || 0}
          subValue={stats?.active_users === 0 && stats?.inactive_users === 0 && stats?.very_inactive_users === 0 && (stats?.never_requested || 0) > 0 ? "计算中..." : "7-30天内有请求"}
          icon={Clock}
          color="yellow"
          onClick={() => { setActivityFilter('inactive'); setPage(1) }}
          className={cn(activityFilter === 'inactive' && "ring-2 ring-primary ring-offset-2")}
        />
        <StatCard
          title="非常不活跃"
          value={stats?.very_inactive_users || 0}
          subValue={stats?.active_users === 0 && stats?.inactive_users === 0 && stats?.very_inactive_users === 0 && (stats?.never_requested || 0) > 0 ? "计算中..." : "超过30天无请求"}
          icon={UserX}
          color="red"
          onClick={() => { setActivityFilter('very_inactive'); setPage(1) }}
          className={cn(activityFilter === 'very_inactive' && "ring-2 ring-primary ring-offset-2")}
        />
        <StatCard
          title="从未请求"
          value={stats?.never_requested || 0}
          subValue="注册后未使用"
          icon={Users}
          color="gray"
          onClick={() => { setActivityFilter('never'); setPage(1) }}
          className={cn(activityFilter === 'never' && "ring-2 ring-primary ring-offset-2")}
        />
      </div>

      {/* Search and Filter */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base font-medium flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Search className="w-4 h-4" />
              用户列表
              <span className="ml-2 text-sm font-normal text-muted-foreground">共 {total} 个</span>
            </div>
            {activityFilter !== 'all' && (
              <Button variant="ghost" size="sm" onClick={() => { setActivityFilter('all'); setPage(1) }} className="h-8 text-xs">
                清除筛选: {activityFilter === 'active' ? '活跃' : activityFilter === 'inactive' ? '不活跃' : activityFilter === 'very_inactive' ? '非常不活跃' : '从未请求'}
              </Button>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col sm:flex-row gap-4 mb-4">
            <div className="flex-1 flex gap-2">
              <div className="relative flex-1 max-w-sm">
                <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input
                  placeholder="搜索用户名/邮箱/LinuxDoID/邀请码..."
                  value={searchInput}
                  onChange={(e) => setSearchInput(e.target.value)}
                  onKeyPress={handleKeyPress}
                  className="pl-9"
                />
              </div>
              <Button onClick={handleSearch}>搜索</Button>
            </div>
            <div className="w-full sm:w-40">
              <Select value={activityFilter} onChange={(e) => { setActivityFilter(e.target.value); setPage(1) }}>
                <option value="all">所有状态</option>
                <option value="active">活跃用户</option>
                <option value="inactive">不活跃用户</option>
                <option value="very_inactive">非常不活跃</option>
                <option value="never">从未请求</option>
              </Select>
            </div>
            <div className="w-full sm:w-36">
              <Select value={groupFilter} onChange={(e) => { setGroupFilter(e.target.value); setPage(1) }}>
                <option value="">所有分组</option>
                {groups.map((g) => (
                  <option key={g.group_name} value={g.group_name}>
                    {g.group_name}
                  </option>
                ))}
              </Select>
            </div>
            <div className="w-full sm:w-36">
              <Select value={sourceFilter} onChange={(e) => { setSourceFilter(e.target.value); setPage(1) }}>
                <option value="">所有来源</option>
                {Object.entries(SOURCE_LABELS).map(([key, info]) => (
                  <option key={key} value={key}>{info.label}</option>
                ))}
              </Select>
            </div>
          </div>

          {/* Users Table */}
          {loading && !users.length ? (
            <div className="flex justify-center py-12">
              <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
          ) : users.length > 0 ? (
            <div className="rounded-md border">
              <Table>
                <TableHeader className="bg-muted/50">
                  <TableRow>
                    <TableHead className="w-16">ID</TableHead>
                    <TableHead>用户</TableHead>
                    <TableHead className="hidden sm:table-cell">角色</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead className="hidden lg:table-cell">Linux.do</TableHead>
                    <TableHead className="text-right">额度 (USD)</TableHead>
                    <TableHead className="text-right hidden sm:table-cell">已用</TableHead>
                    <TableHead className="text-right hidden md:table-cell">请求数</TableHead>
                    <TableHead className="hidden md:table-cell">最后请求</TableHead>
                    <TableHead>活跃度</TableHead>
                    <TableHead className="w-20">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((user) => (
                    <TableRow key={user.id} className="hover:bg-muted/50 transition-colors group">
                      <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">{user.id}</TableCell>
                      <TableCell>
                        <div
                          className="flex items-center gap-3 px-3 py-2 rounded-xl bg-muted/30 hover:bg-primary/5 transition-all cursor-pointer border border-transparent hover:border-primary/20 w-max min-w-[180px]"
                          onClick={() => openUserAnalysis(user.id, user.username)}
                          title="查看用户分析"
                        >
                          <div className="w-8 h-8 rounded-full bg-primary/10 flex items-center justify-center border border-primary/20 text-sm text-primary font-bold shrink-0">
                            {user.username[0]?.toUpperCase()}
                          </div>
                          <div className="flex flex-col min-w-0">
                            <span className="font-bold text-sm tracking-tight">{user.username}</span>
                            <div className="flex items-center gap-1.5 mt-0.5">
                              {user.display_name && (
                                <span className="text-[10px] text-muted-foreground">{user.display_name}</span>
                              )}
                              <Badge variant="outline" className="px-1.5 py-0 h-4 text-[9px] font-medium leading-none shrink-0 border-muted-foreground/20">
                                {user.group || 'default'}
                              </Badge>
                            </div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell className="hidden sm:table-cell">
                        {getRoleBadge(user.role)}
                      </TableCell>
                      <TableCell>{getStatusBadge(user.status)}</TableCell>
                      <TableCell className="hidden lg:table-cell">
                        {user.linux_do_id ? (
                          <button
                            onClick={async () => {
                              const lid = user.linux_do_id
                              if (!lid || linuxDoLookupLoading) return
                              setLinuxDoLookupLoading(lid)
                              try {
                                const res = await fetch(`${apiUrl}/api/linuxdo/lookup/${encodeURIComponent(lid)}`, { headers: getAuthHeaders() })
                                const data = await res.json()
                                if (data.success && data.data?.profile_url) {
                                  window.open(data.data.profile_url, '_blank')
                                } else if (data.error_type === 'rate_limit') {
                                  showToast('error', data.message || `请求被限速，请等待 ${data.wait_seconds || '?'} 秒后重试`)
                                } else if (data.fallback_url) {
                                  window.open(data.fallback_url, '_blank')
                                  showToast('info', '服务器查询失败，已在新标签页打开 Linux.do 证书页面')
                                } else {
                                  showToast('error', data.message || '查询 Linux.do 用户名失败')
                                }
                              } catch { showToast('error', '查询 Linux.do 用户名失败') }
                              finally { setLinuxDoLookupLoading(null) }
                            }}
                            disabled={linuxDoLookupLoading === user.linux_do_id}
                            className="text-xs font-mono text-blue-500 hover:text-blue-600 hover:underline disabled:opacity-50 cursor-pointer"
                            title="点击查看 Linux.do 用户主页"
                          >
                            {linuxDoLookupLoading === user.linux_do_id ? '查询中...' : user.linux_do_id}
                          </button>
                        ) : (
                          <span className="text-xs text-muted-foreground">-</span>
                        )}
                      </TableCell>
                      <TableCell className="text-right font-mono text-sm font-bold text-primary tabular-nums tracking-tight">
                        {formatQuota(user.quota)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs text-muted-foreground hidden sm:table-cell tabular-nums">
                        {formatQuota(user.used_quota)}
                      </TableCell>
                      <TableCell className="text-right hidden md:table-cell tabular-nums font-bold text-sm">
                        {user.request_count.toLocaleString()}
                      </TableCell>
                      <TableCell className="hidden md:table-cell text-xs whitespace-nowrap tabular-nums text-muted-foreground">{formatLastRequest(user)}</TableCell>
                      <TableCell>{getActivityBadge(user.activity_level)}</TableCell>
                      <TableCell>
                        <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-blue-500 hover:text-blue-600 hover:bg-blue-500/10 h-7 w-7 p-0"
                            onClick={() => openUserAnalysis(user.id, user.username)}
                            title="用户分析"
                          >
                            <Eye className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <div className="py-20 text-center text-muted-foreground bg-muted/10 rounded-lg border border-dashed">
              <Users className="mx-auto h-10 w-10 mb-3 opacity-20" />
              <p>{search || activityFilter !== 'all' ? '没有找到符合条件的用户' : '暂无用户数据'}</p>
            </div>
          )}

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="flex items-center justify-between mt-4 px-2">
              <p className="text-sm text-muted-foreground">
                第 {page} / {totalPages} 页
              </p>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setPage(p => Math.max(1, p - 1))}
                  disabled={page === 1}
                >
                  <ChevronLeft className="h-4 w-4 mr-1" />
                  上一页
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                  disabled={page === totalPages}
                >
                  下一页
                  <ChevronRight className="h-4 w-4 ml-1" />
                </Button>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* User Analysis Dialog */}
      {selectedUser && (
        <UserAnalysisDialog
          open={analysisDialogOpen}
          onOpenChange={setAnalysisDialogOpen}
          userId={selectedUser.id}
          username={selectedUser.username}
          onBanned={() => fetchUsers()}
          onUnbanned={() => fetchUsers()}
          renderExtra={() => (
            <div className="space-y-3">
              <h4 className="text-sm font-semibold text-muted-foreground flex items-center gap-2">
                <Users className="w-4 h-4" />
                邀请用户
                {invitedUsers?.inviter?.aff_code && (
                  <Badge variant="outline" className="text-xs px-1.5 py-0 font-mono">
                    邀请码: {invitedUsers.inviter.aff_code}
                  </Badge>
                )}
                {invitedUsers?.stats && invitedUsers.stats.total_invited > 0 && (
                  <Badge variant="secondary" className="text-xs px-1.5 py-0">
                    共 {invitedUsers.stats.total_invited} 人
                  </Badge>
                )}
              </h4>

              {invitedLoading ? (
                <div className="flex items-center justify-center py-6">
                  <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                </div>
              ) : invitedUsers?.items && invitedUsers.items.length > 0 ? (
                <>
                  {/* 邀请统计 */}
                  <div className="grid grid-cols-4 gap-2">
                    <div className="rounded-lg border bg-muted/30 p-2 text-center">
                      <div className="text-sm font-bold">{invitedUsers.stats.total_invited}</div>
                      <div className="text-xs text-muted-foreground">邀请总数</div>
                    </div>
                    <div className="rounded-lg border bg-green-50 dark:bg-green-900/20 p-2 text-center">
                      <div className="text-sm font-bold text-green-600">{invitedUsers.stats.active_count}</div>
                      <div className="text-xs text-muted-foreground">活跃用户</div>
                    </div>
                    <div className={cn(
                      "rounded-lg border p-2 text-center",
                      invitedUsers.stats.banned_count > 0 ? "bg-red-50 dark:bg-red-900/20" : "bg-muted/30"
                    )}>
                      <div className={cn("text-sm font-bold", invitedUsers.stats.banned_count > 0 && "text-red-600")}>{invitedUsers.stats.banned_count}</div>
                      <div className="text-xs text-muted-foreground">已封禁</div>
                    </div>
                    <div className="rounded-lg border bg-muted/30 p-2 text-center">
                      <div className="text-sm font-bold">{(invitedUsers.stats.total_used_quota / 500000).toFixed(2)}</div>
                      <div className="text-xs text-muted-foreground">总消耗 $</div>
                    </div>
                  </div>

                  {/* 邀请用户列表 */}
                  <div className="rounded-lg border overflow-hidden">
                    <Table>
                      <TableHeader>
                        <TableRow className="h-8 bg-muted/50 hover:bg-muted/50">
                          <TableHead className="h-8 text-xs w-[60px]">ID</TableHead>
                          <TableHead className="h-8 text-xs">用户名</TableHead>
                          <TableHead className="h-8 text-xs w-[60px]">状态</TableHead>
                          <TableHead className="h-8 text-xs text-right">请求数</TableHead>
                          <TableHead className="h-8 text-xs text-right">消耗 $</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {invitedUsers.items.map((u) => (
                          <TableRow key={u.user_id} className="h-8 hover:bg-muted/30">
                            <TableCell className="py-1.5 text-xs text-muted-foreground font-mono">{u.user_id}</TableCell>
                            <TableCell className="py-1.5 text-xs">
                              <span className="font-medium">{u.username}</span>
                              {u.display_name && <span className="text-muted-foreground ml-1">({u.display_name})</span>}
                            </TableCell>
                            <TableCell className="py-1.5 text-xs">
                              {u.status === 2 ? (
                                <Badge variant="destructive" className="text-xs px-1 py-0">禁用</Badge>
                              ) : (
                                <Badge variant="success" className="text-xs px-1 py-0">正常</Badge>
                              )}
                            </TableCell>
                            <TableCell className="py-1.5 text-xs text-right tabular-nums">{u.request_count.toLocaleString()}</TableCell>
                            <TableCell className="py-1.5 text-xs text-right tabular-nums font-mono">{(u.used_quota / 500000).toFixed(2)}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>

                  {/* 分页 */}
                  {invitedUsers.total > 10 && (
                    <div className="flex items-center justify-between pt-2">
                      <span className="text-xs text-muted-foreground">
                        第 {invitedPage} 页，共 {Math.ceil(invitedUsers.total / 10)} 页
                      </span>
                      <div className="flex gap-1">
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() => setInvitedPage(p => Math.max(1, p - 1))}
                          disabled={invitedPage === 1}
                        >
                          <ChevronLeft className="h-3 w-3" />
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() => setInvitedPage(p => p + 1)}
                          disabled={invitedPage >= Math.ceil(invitedUsers.total / 10)}
                        >
                          <ChevronRight className="h-3 w-3" />
                        </Button>
                      </div>
                    </div>
                  )}
                </>
              ) : (
                <div className="text-xs text-muted-foreground italic py-4 text-center border rounded-lg bg-muted/10">
                  该用户暂无邀请记录
                </div>
              )}
            </div>
          )}
        />
      )}
        </TabsContent>

        <TabsContent value="affiliate" className="mt-6">
          <AffiliateStats />
        </TabsContent>

        <TabsContent value="panel-whitelist" className="mt-6">
          <PanelWhitelist />
        </TabsContent>
      </Tabs>
    </div>
  )
}
